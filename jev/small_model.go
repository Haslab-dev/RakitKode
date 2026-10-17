package jev

import (
	"math"
	"strings"
	"sync"
	"time"
)

// FullDecision aggregates all multi-head decisions evaluated in a single forward pass.
type FullDecision struct {
	Category       string    `json:"category"`
	CategoryConf   float64   `json:"category_confidence"`
	Role           string    `json:"role"`
	RoleConf       float64   `json:"role_confidence"`
	NeedsTools     float64   `json:"needs_tools"`
	SafetyRisk     RiskLevel `json:"safety_risk"`
	RiskConf       float64   `json:"risk_confidence"`
	ShouldDelegate bool      `json:"should_delegate"`
	DelegateConf   float64   `json:"delegate_confidence"`
	LatencyUs      int64     `json:"latency_us"`
	Model          string    `json:"model"`
}

// SmallNeuralEngine performs sub-millisecond in-process neural inference
// for agent task routing, role determination, tool safety risk auditing,
// and delegation using weights trained via RLCD (Reinforcement Learning
// for Calibrated Decisions) in scripts/train_small_model.py.
// It has zero external dependencies, requires no local server/Ollama,
// and runs whenever Jev API key is missing or Jev API returns an error.
type SmallNeuralEngine struct {
	mu         sync.RWMutex
	vocabMap   map[string]int
	categories []string
	roles      []string
	risks      []string
	stats      SmallModelStats
}

// SmallModelStats tracks metrics for the small neural network inference.
type SmallModelStats struct {
	TotalInferences int            `json:"total_inferences"`
	CategoryCounts  map[string]int `json:"category_counts"`
	RoleCounts      map[string]int `json:"role_counts"`
	RiskCounts      map[string]int `json:"risk_counts"`
	AvgLatencyUs    float64        `json:"avg_latency_us"`
	totalLatencyUs  int64
}

// NewSmallNeuralEngine instantiates the embedded neural engine with compiled RLCD weights.
func NewSmallNeuralEngine() *SmallNeuralEngine {
	vMap := make(map[string]int, len(defaultVocabulary))
	for i, w := range defaultVocabulary {
		vMap[strings.ToLower(w)] = i
	}
	return &SmallNeuralEngine{
		vocabMap:   vMap,
		categories: defaultCategories,
		roles:      defaultRoles,
		risks:      defaultRisks,
		stats: SmallModelStats{
			CategoryCounts: make(map[string]int),
			RoleCounts:     make(map[string]int),
			RiskCounts:     make(map[string]int),
		},
	}
}

// ExtractFeatures converts raw prompt text into a 256-dimensional normalized feature vector
// using deterministic FNV-1a hashing.
func (s *SmallNeuralEngine) ExtractFeatures(text string) []float32 {
	vec := make([]float32, SmallModelInputDim)
	lower := strings.ToLower(text)
	words := strings.Fields(lower)

	// 1. Vocabulary keyword presence with word boundary check
	for i, w := range defaultVocabulary {
		if isWordMatch(lower, w) {
			vec[i] += 1.5
		}
	}

	// 2. Character trigram hashing into remaining slots
	offset := len(defaultVocabulary)
	numHashSlots := SmallModelInputDim - offset
	if numHashSlots > 0 {
		for _, w := range words {
			runes := []rune(w)
			if len(runes) >= 3 {
				for j := 0; j <= len(runes)-3; j++ {
					tri := string(runes[j : j+3])
					h := fnv1aHash(tri) % uint32(numHashSlots)
					vec[offset+int(h)] += 0.5
				}
			}
		}
	}

	// 3. L2 Normalization
	var sumSq float64
	for _, v := range vec {
		sumSq += float64(v * v)
	}
	norm := float32(math.Sqrt(sumSq))
	if norm > 1e-6 {
		for i := range vec {
			vec[i] /= norm
		}
	}

	return vec
}

// ForwardRLCD computes the multi-head neural forward pass with RLCD temperature scaling.
func (s *SmallNeuralEngine) ForwardRLCD(x []float32) (catProbs []float32, needsTools float32, roleProbs []float32, riskProbs []float32, delegateProb float32) {
	// Hidden layer: 64 units
	h1 := make([]float32, SmallModelHiddenDim)
	for i := 0; i < SmallModelHiddenDim; i++ {
		var sum float32 = weightsB1[i]
		row := weightsW1[i]
		for j := 0; j < SmallModelInputDim; j++ {
			sum += row[j] * x[j]
		}
		if sum > 0 {
			h1[i] = sum
		} else {
			h1[i] = 0
		}
	}

	temp := float32(SmallModelTemperature)
	if temp <= 0 {
		temp = 1.0
	}

	// 1. Category Head: 9 classes
	catLogits := make([]float32, SmallModelNumCats)
	for i := 0; i < SmallModelNumCats; i++ {
		var sum float32 = weightsBCat[i]
		row := weightsWCat[i]
		for j := 0; j < SmallModelHiddenDim; j++ {
			sum += row[j] * h1[j]
		}
		catLogits[i] = sum / temp
	}
	catProbs = softmaxF32(catLogits)

	// 2. NeedsTools Head: 1 sigmoid output
	var toolsLogit float32 = weightsBTools
	for j := 0; j < SmallModelHiddenDim; j++ {
		toolsLogit += weightsWTools[j] * h1[j]
	}
	needsTools = sigmoidF32(toolsLogit / temp)

	// 3. Role Head: 5 classes
	roleLogits := make([]float32, SmallModelNumRoles)
	for i := 0; i < SmallModelNumRoles; i++ {
		var sum float32 = weightsBRole[i]
		row := weightsWRole[i]
		for j := 0; j < SmallModelHiddenDim; j++ {
			sum += row[j] * h1[j]
		}
		roleLogits[i] = sum / temp
	}
	roleProbs = softmaxF32(roleLogits)

	// 4. Safety Risk Head: 3 classes (safe, medium, critical)
	riskLogits := make([]float32, SmallModelNumRisks)
	for i := 0; i < SmallModelNumRisks; i++ {
		var sum float32 = weightsBRisk[i]
		row := weightsWRisk[i]
		for j := 0; j < SmallModelHiddenDim; j++ {
			sum += row[j] * h1[j]
		}
		riskLogits[i] = sum / temp
	}
	riskProbs = softmaxF32(riskLogits)

	// 5. Delegation Head: 1 sigmoid output
	var delLogit float32 = weightsBDelegate
	for j := 0; j < SmallModelHiddenDim; j++ {
		delLogit += weightsWDelegate[j] * h1[j]
	}
	delegateProb = sigmoidF32(delLogit / temp)

	return catProbs, needsTools, roleProbs, riskProbs, delegateProb
}

// Forward maintains backward compatibility for callers expecting the original 3 heads.
func (s *SmallNeuralEngine) Forward(x []float32) (catProbs []float32, needsTools float32, roleProbs []float32) {
	catProbs, needsTools, roleProbs, _, _ = s.ForwardRLCD(x)
	return catProbs, needsTools, roleProbs
}

// PredictCategory routes an agent turn into a tool category with confidence and needsTools score.
func (s *SmallNeuralEngine) PredictCategory(prompt string) (category string, confidence float64, needsTools float64) {
	start := time.Now()
	x := s.ExtractFeatures(prompt)
	catProbs, nt, _, _, _ := s.ForwardRLCD(x)

	bestIdx := 0
	var maxProb float32 = -1.0
	for i, p := range catProbs {
		if p > maxProb {
			maxProb = p
			bestIdx = i
		}
	}

	cat := s.categories[bestIdx]
	conf := float64(maxProb)
	dur := time.Since(start)

	s.mu.Lock()
	s.stats.TotalInferences++
	s.stats.CategoryCounts[cat]++
	us := dur.Microseconds()
	s.stats.totalLatencyUs += us
	s.stats.AvgLatencyUs = float64(s.stats.totalLatencyUs) / float64(s.stats.TotalInferences)
	s.mu.Unlock()

	return cat, conf, float64(nt)
}

// PredictRole predicts the specialist sub-agent role (coder, researcher, reviewer, debugger, devops).
func (s *SmallNeuralEngine) PredictRole(task string) (role string, confidence float64) {
	start := time.Now()
	x := s.ExtractFeatures(task)
	_, _, roleProbs, _, _ := s.ForwardRLCD(x)

	bestIdx := 0
	var maxProb float32 = -1.0
	for i, p := range roleProbs {
		if p > maxProb {
			maxProb = p
			bestIdx = i
		}
	}

	r := s.roles[bestIdx]
	dur := time.Since(start)

	s.mu.Lock()
	s.stats.TotalInferences++
	s.stats.RoleCounts[r]++
	us := dur.Microseconds()
	s.stats.totalLatencyUs += us
	if s.stats.TotalInferences > 0 {
		s.stats.AvgLatencyUs = float64(s.stats.totalLatencyUs) / float64(s.stats.TotalInferences)
	}
	s.mu.Unlock()

	return r, float64(maxProb)
}

// PredictSafetyRisk predicts the execution safety risk level (safe, medium, critical).
func (s *SmallNeuralEngine) PredictSafetyRisk(commandOrPrompt string) (riskLevel RiskLevel, confidence float64) {
	x := s.ExtractFeatures(commandOrPrompt)
	_, _, _, riskProbs, _ := s.ForwardRLCD(x)

	bestIdx := 0
	var maxProb float32 = -1.0
	for i, p := range riskProbs {
		if p > maxProb {
			maxProb = p
			bestIdx = i
		}
	}

	raw := s.risks[bestIdx]
	switch raw {
	case "critical":
		riskLevel = RiskCritical
	case "medium":
		riskLevel = RiskMedium
	default:
		riskLevel = RiskSafe
	}

	s.mu.Lock()
	if s.stats.RiskCounts == nil {
		s.stats.RiskCounts = make(map[string]int)
	}
	s.stats.RiskCounts[string(riskLevel)]++
	s.mu.Unlock()

	return riskLevel, float64(maxProb)
}

// PredictDelegation predicts whether a task warrants sub-agent delegation.
func (s *SmallNeuralEngine) PredictDelegation(task string) (shouldDelegate bool, confidence float64) {
	x := s.ExtractFeatures(task)
	_, _, _, _, delProb := s.ForwardRLCD(x)
	conf := float64(delProb)
	return conf >= 0.5, conf
}

// PredictFullDecision evaluates all 5 RLCD decision heads in a single forward pass (<40 µs).
func (s *SmallNeuralEngine) PredictFullDecision(prompt string) FullDecision {
	start := time.Now()
	x := s.ExtractFeatures(prompt)
	catProbs, nt, roleProbs, riskProbs, delProb := s.ForwardRLCD(x)

	// 1. Best category
	bestCatIdx := 0
	var maxCatProb float32 = -1.0
	for i, p := range catProbs {
		if p > maxCatProb {
			maxCatProb = p
			bestCatIdx = i
		}
	}

	// 2. Best role
	bestRoleIdx := 0
	var maxRoleProb float32 = -1.0
	for i, p := range roleProbs {
		if p > maxRoleProb {
			maxRoleProb = p
			bestRoleIdx = i
		}
	}

	// 3. Best risk
	bestRiskIdx := 0
	var maxRiskProb float32 = -1.0
	for i, p := range riskProbs {
		if p > maxRiskProb {
			maxRiskProb = p
			bestRiskIdx = i
		}
	}

	rawRisk := s.risks[bestRiskIdx]
	var riskLevel RiskLevel
	switch rawRisk {
	case "critical":
		riskLevel = RiskCritical
	case "medium":
		riskLevel = RiskMedium
	default:
		riskLevel = RiskSafe
	}

	dur := time.Since(start)

	return FullDecision{
		Category:       s.categories[bestCatIdx],
		CategoryConf:   float64(maxCatProb),
		Role:           s.roles[bestRoleIdx],
		RoleConf:       float64(maxRoleProb),
		NeedsTools:     float64(nt),
		SafetyRisk:     riskLevel,
		RiskConf:       float64(maxRiskProb),
		ShouldDelegate: delProb >= 0.5,
		DelegateConf:   float64(delProb),
		LatencyUs:      dur.Microseconds(),
		Model:          SmallModelVersion,
	}
}

// GetStats returns inference count and latency telemetry.
func (s *SmallNeuralEngine) GetStats() SmallModelStats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	cp := s.stats
	cp.CategoryCounts = make(map[string]int, len(s.stats.CategoryCounts))
	for k, v := range s.stats.CategoryCounts {
		cp.CategoryCounts[k] = v
	}
	cp.RoleCounts = make(map[string]int, len(s.stats.RoleCounts))
	for k, v := range s.stats.RoleCounts {
		cp.RoleCounts[k] = v
	}
	cp.RiskCounts = make(map[string]int, len(s.stats.RiskCounts))
	for k, v := range s.stats.RiskCounts {
		cp.RiskCounts[k] = v
	}
	return cp
}

// Helper mathematical functions in float32
func softmaxF32(logits []float32) []float32 {
	var maxVal float32 = -1e9
	for _, v := range logits {
		if v > maxVal {
			maxVal = v
		}
	}
	probs := make([]float32, len(logits))
	var sum float32
	for i, v := range logits {
		e := float32(math.Exp(float64(v - maxVal)))
		probs[i] = e
		sum += e
	}
	if sum > 0 {
		for i := range probs {
			probs[i] /= sum
		}
	}
	return probs
}

func sigmoidF32(x float32) float32 {
	if x > 15 {
		return 1.0
	}
	if x < -15 {
		return 0.0
	}
	return float32(1.0 / (1.0 + math.Exp(-float64(x))))
}

func fnv1aHash(s string) uint32 {
	var h uint32 = 2166136261
	for i := 0; i < len(s); i++ {
		h ^= uint32(s[i])
		h *= 16777619
	}
	return h
}

func isWordMatch(text, word string) bool {
	if strings.Contains(word, " ") {
		return strings.Contains(text, word)
	}
	idx := strings.Index(text, word)
	for idx != -1 {
		startOk := idx == 0 || !isAlphaNumByte(text[idx-1])
		endIdx := idx + len(word)
		endOk := endIdx == len(text) || !isAlphaNumByte(text[endIdx])
		if startOk && endOk {
			return true
		}
		next := strings.Index(text[idx+1:], word)
		if next == -1 {
			break
		}
		idx += 1 + next
	}
	return false
}

func isAlphaNumByte(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}
