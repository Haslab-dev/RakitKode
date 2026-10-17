// Package jev provides backward-compatible aliases to the primary decisions package.
// All decision logic is implemented in github.com/Haslab-dev/RakitKode/decisions.
package jev

import (
	"github.com/Haslab-dev/RakitKode/decisions"
)

type (
	Router               = decisions.Router
	JevRouter            = decisions.JevRouter
	RouteDecision        = decisions.RouteDecision
	RoleDecision         = decisions.RoleDecision
	SubagentCandidate    = decisions.SubagentCandidate
	SubagentDecision     = decisions.SubagentDecision
	RouterStats          = decisions.RouterStats
	RiskLevel            = decisions.RiskLevel
	SafetyAssessment     = decisions.SafetyAssessment
	LocalDecisionEngine  = decisions.LocalDecisionEngine
	LocalStats           = decisions.LocalStats
	FullDecision         = decisions.FullDecision
	SmallNeuralEngine    = decisions.SmallNeuralEngine
	SmallModelStats      = decisions.SmallModelStats
	DecisionMode         = decisions.DecisionMode
	DecisionConfig       = decisions.DecisionConfig
	DecisionStatus       = decisions.DecisionStatus
	DecisionAdapter      = decisions.DecisionAdapter
	DecisionsFileAdapter = decisions.DecisionsFileAdapter
	FileAdapter          = decisions.FileAdapter
	LatencyStats         = decisions.LatencyStats
	DecisionPair         = decisions.DecisionPair
	BenchmarkReport      = decisions.BenchmarkReport
)

const (
	ModeCascade       = decisions.ModeCascade
	ModeJevOnly       = decisions.ModeJevOnly
	ModeLocalOnly     = decisions.ModeLocalOnly
	RiskSafe          = decisions.RiskSafe
	RiskMedium        = decisions.RiskMedium
	RiskCritical      = decisions.RiskCritical
	SmallModelVersion = decisions.SmallModelVersion
)

var (
	NewRouter                 = decisions.NewRouter
	NewJevRouter              = decisions.NewJevRouter
	NewLocalDecisionEngine    = decisions.NewLocalDecisionEngine
	NewSmallNeuralEngine      = decisions.NewSmallNeuralEngine
	NewDecisionsFileAdapter   = decisions.NewDecisionsFileAdapter
	NewFileAdapter            = decisions.NewFileAdapter
	DefaultDecisionConfig     = decisions.DefaultDecisionConfig
	HeuristicRoleDecision     = decisions.HeuristicRoleDecision
	HeuristicSubagentDecision = decisions.HeuristicSubagentDecision
	RunBenchmarkSuite         = decisions.RunBenchmarkSuite
)
