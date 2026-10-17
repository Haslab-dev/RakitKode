#!/usr/bin/env python3
"""
train_small_model.py
Trains an in-process Reinforcement Learning for Calibrated Decisions (RLCD) Neural Model
for RakitKode's local smart decision engine.

Features:
- Subword & word token feature mapping (256-dim feature vector) with FNV-1a deterministic hashing
- Multi-Head Neural Network (256 -> 64 -> Category / Role / NeedsTools / SafetyRisk / Delegation heads)
- Trained with RLCD Proper Scoring Rules: Hybrid Cross-Entropy + Brier Score Loss for strict calibration
- Post-training Temperature Scaling to minimize Expected Calibration Error (ECE)
- Exports model weights and temperature scaling directly to Go source code: jev/weights_generated.go
"""

import json
import math
import os
import re
import sys
import numpy as np

# Decision Output Spaces
CATEGORIES = [
    "explore",   # 0
    "read",      # 1
    "modify",    # 2
    "execute",   # 3
    "git",       # 4
    "web",       # 5
    "skills",    # 6
    "mcp",       # 7
    "none"       # 8
]

ROLES = [
    "coder",       # 0
    "researcher",  # 1
    "reviewer",    # 2
    "debugger",    # 3
    "devops"       # 4
]

RISK_LEVELS = [
    "safe",      # 0 - read-only, inspection, conversational
    "medium",    # 1 - code edits, builds, local tests, standard commits
    "critical"   # 2 - destructive operations (rm -rf, sudo, force push, drop table)
]

VOCABULARY = [
    # Explore / search
    "find", "search", "locate", "where", "list", "grep", "glob", "files", "dir", "tree", "explore",
    "structure", "inspect", "show me all", "which file", "path", "overview", "hierarchy",
    # Read
    "read", "cat", "view", "open", "display", "check content", "look at", "examine", "show code",
    "contents of", "header", "lines", "source", "definition", "implementation",
    # Modify / write / edit
    "edit", "modify", "replace", "write", "create", "update", "patch", "refactor", "fix bug",
    "implement", "change", "delete file", "insert", "append", "rewrite", "add function", "rename",
    # Execute / bash
    "run", "exec", "bash", "command", "build", "compile", "test", "go test", "npm test",
    "cargo build", "start server", "execute", "launch", "kill", "process", "terminal", "output",
    # Git
    "git", "commit", "push", "pull", "branch", "diff", "status", "checkout", "stash",
    "merge", "rebase", "log", "remote", "tag", "stage", "uncommitted", "repo",
    # Web / browse
    "http", "https", "fetch", "url", "web", "download", "scrape", "browse", "api request", "curl", "endpoint",
    "website", "html", "documentation", "doc", "docs", "online",
    # Skills / Plugins
    "skill", "skills", "learn", "how to", "install skill", "use skill", "custom skill", "plugin", "workflow", "recipe",
    # MCP
    "mcp", "server", "tool protocol", "call mcp", "mcp tool", "database query", "sqlite", "postgres",
    # Conversational / none
    "hello", "hi", "hey", "who are you", "what can you do", "thanks", "thank you", "bye",
    "explain", "summarize", "tell me about", "what is", "why", "how does", "help",
    # Programming concepts
    "function", "class", "struct", "interface", "method", "variable", "package", "import",
    "type", "error", "nil", "null", "pointer", "goroutine", "channel", "thread", "memory",
    "leak", "crash", "panic", "segfault", "benchmark", "optimize", "slow", "performance",
    # Safety keywords (Risk Critical)
    "rm -rf", "delete all", "drop table", "truncate", "format disk", "sudo", "chmod 777",
    "force push", "git push --force", "api_key", "secret", "password", "token", "env", "credentials", "id_rsa", "auth"
]

def fnv1a_32(s: str) -> int:
    h = 2166136261
    for b in s.encode('utf-8'):
        h ^= b
        h = (h * 16777619) & 0xFFFFFFFF
    return h

FEATURE_SIZE = 256
vocab_map = {word: i for i, word in enumerate(VOCABULARY)}

# Dataset schema: (prompt, category, needs_tools, role, risk_level, subagent_needed)
DATASET = [
    # Explore
    ("find all files matching *.go in the repository", "explore", 1.0, "researcher", "safe", 0.0),
    ("where is the entrypoint defined in this project?", "explore", 1.0, "researcher", "safe", 0.0),
    ("list all files in internal/gateway directory", "explore", 1.0, "researcher", "safe", 0.0),
    ("search for function ExecuteTurnWithModel across the codebase", "explore", 1.0, "researcher", "safe", 0.0),
    ("locate all places where Jev router is instantiated", "explore", 1.0, "researcher", "safe", 0.0),
    ("show me the project directory tree and structure", "explore", 1.0, "researcher", "safe", 0.0),
    ("grep for error handling in agent engine", "explore", 1.0, "researcher", "safe", 0.0),
    ("find where the config is loaded", "explore", 1.0, "researcher", "safe", 0.0),
    ("investigate and audit the codebase for potential security leaks", "explore", 1.0, "researcher", "safe", 1.0),
    ("research and trace symbols across the project", "explore", 1.0, "researcher", "safe", 0.0),
    ("audit codebase for vulnerabilities and security leaks", "explore", 1.0, "researcher", "safe", 1.0),

    # Read
    ("read the contents of main.go", "read", 1.0, "coder", "safe", 0.0),
    ("view file internal/gateway/subagent.go line 1 to 50", "read", 1.0, "coder", "safe", 0.0),
    ("show me what is inside package.json", "read", 1.0, "coder", "safe", 0.0),
    ("check the implementation of Router in jev/jev.go", "read", 1.0, "coder", "safe", 0.0),
    ("display the README.md file", "read", 1.0, "researcher", "safe", 0.0),
    ("examine the go.mod dependencies", "read", 1.0, "coder", "safe", 0.0),
    ("cat the error log file", "read", 1.0, "debugger", "safe", 0.0),
    ("open and inspect internal/config/config.go", "read", 1.0, "coder", "safe", 0.0),

    # Modify
    ("edit main.go to update version string to v0.2.8", "modify", 1.0, "coder", "medium", 0.0),
    ("write a new unit test in jev/local_test.go", "modify", 1.0, "coder", "medium", 0.0),
    ("refactor the subagent router to handle nil checks", "modify", 1.0, "coder", "medium", 0.0),
    ("fix bug in turn execution loop where context cancelled was ignored", "modify", 1.0, "debugger", "medium", 0.0),
    ("create a new file scripts/deploy.sh", "modify", 1.0, "devops", "medium", 0.0),
    ("patch the memory leak in buffer pool", "modify", 1.0, "debugger", "medium", 0.0),
    ("add a new method to Router struct", "modify", 1.0, "coder", "medium", 0.0),
    ("replace deprecated function with the new API", "modify", 1.0, "coder", "medium", 0.0),
    ("implement token estimation calculator", "modify", 1.0, "coder", "medium", 0.0),
    ("conduct full system architectural refactor of memory management", "modify", 1.0, "coder", "medium", 1.0),

    # Execute
    ("run go test ./... to check all unit tests", "execute", 1.0, "devops", "safe", 0.0),
    ("build the binary with go build ./cmd/rakit", "execute", 1.0, "devops", "safe", 0.0),
    ("execute npm run build", "execute", 1.0, "devops", "safe", 0.0),
    ("compile the wails app", "execute", 1.0, "devops", "safe", 0.0),
    ("run the benchmarks on model inference", "execute", 1.0, "devops", "safe", 0.0),
    ("execute bash command ls -la", "execute", 1.0, "devops", "safe", 0.0),
    ("start the local test server on port 8080", "execute", 1.0, "devops", "safe", 0.0),
    ("run linter oxlint and check code style", "execute", 1.0, "reviewer", "safe", 0.0),
    ("rm -rf / --no-preserve-root", "execute", 1.0, "devops", "critical", 0.0),
    ("sudo chmod -R 777 /var/data && drop table users", "execute", 1.0, "devops", "critical", 0.0),
    ("rm -rf /var/cache/app && rm -rf /tmp/build", "execute", 1.0, "devops", "critical", 0.0),

    # Git
    ("git commit -m 'feat: update version' and push to origin", "git", 1.0, "devops", "medium", 0.0),
    ("show git status and git diff", "git", 1.0, "reviewer", "safe", 0.0),
    ("create a new git branch feat/small-model", "git", 1.0, "devops", "safe", 0.0),
    ("check git log for the last 5 commits", "git", 1.0, "reviewer", "safe", 0.0),
    ("tag this release with v0.2.8 and push tags", "git", 1.0, "devops", "medium", 0.0),
    ("git checkout main and pull latest changes", "git", 1.0, "devops", "safe", 0.0),
    ("stash current changes in git", "git", 1.0, "coder", "safe", 0.0),
    ("git push origin main --force", "git", 1.0, "devops", "critical", 0.0),

    # Web
    ("fetch documentation from https://golang.org/doc", "web", 1.0, "researcher", "safe", 0.0),
    ("fetch documentation from https://golang.org/pkg/net/http", "web", 1.0, "researcher", "safe", 0.0),
    ("curl the API endpoint and inspect response", "web", 1.0, "researcher", "safe", 0.0),
    ("download the latest release assets from github", "web", 1.0, "devops", "safe", 0.0),
    ("browse the web to research agent harness architecture", "web", 1.0, "researcher", "safe", 1.0),
    ("scrape the release notes page from website", "web", 1.0, "researcher", "safe", 0.0),
    ("http request to remote documentation", "web", 1.0, "researcher", "safe", 0.0),

    # Skills
    ("install skill for impeccble UI design", "skills", 1.0, "coder", "safe", 0.0),
    ("use custom skill to inspect and design frontend components", "skills", 1.0, "coder", "safe", 0.0),
    ("how do I create a new custom skill?", "skills", 0.0, "coder", "safe", 0.0),
    ("show me available skills in the catalog", "skills", 1.0, "researcher", "safe", 0.0),
    ("run workflow using installed skills", "skills", 1.0, "coder", "safe", 0.0),

    # MCP
    ("query postgres database via mcp server", "mcp", 1.0, "coder", "safe", 0.0),
    ("call mcp tool filesystem list_allowed_directories", "mcp", 1.0, "devops", "safe", 0.0),
    ("call mcp tool to inspect external resource", "mcp", 1.0, "devops", "safe", 0.0),
    ("connect to mcp sqlite server and inspect schema", "mcp", 1.0, "coder", "safe", 0.0),

    # None / Conversational / Reasoning
    ("hello, who are you and how can you help me?", "none", 0.0, "researcher", "safe", 0.0),
    ("hello, how are you today?", "none", 0.0, "coder", "safe", 0.0),
    ("hello how are you", "none", 0.0, "coder", "safe", 0.0),
    ("hi how are you doing?", "none", 0.0, "coder", "safe", 0.0),
    ("good morning, how are you today?", "none", 0.0, "coder", "safe", 0.0),
    ("hey assistant, what can you do?", "none", 0.0, "coder", "safe", 0.0),
    ("tell me a joke", "none", 0.0, "coder", "safe", 0.0),
    ("explain the difference between concurrency and parallelism", "none", 0.0, "coder", "safe", 0.0),
    ("what is the time complexity of quicksort?", "none", 0.0, "coder", "safe", 0.0),
    ("summarize the pros and cons of microservices", "none", 0.0, "reviewer", "safe", 0.0),
    ("thank you for your help!", "none", 0.0, "researcher", "safe", 0.0),
    ("why does Go use goroutines instead of OS threads?", "none", 0.0, "coder", "safe", 0.0),
    ("review this code architecture conceptually and give suggestions", "none", 0.0, "reviewer", "safe", 0.0)
]

# Augmented synthetic variations
for base_text, cat, nt, role, risk, delegate in list(DATASET):
    DATASET.append(("please " + base_text, cat, nt, role, risk, delegate))
    DATASET.append(("can you " + base_text + " now?", cat, nt, role, risk, delegate))
    DATASET.append(("I need you to " + base_text, cat, nt, role, risk, delegate))

def is_word_match(text: str, word: str) -> bool:
    if " " in word:
        return word in text
    pattern = r'(?:\b|_)' + re.escape(word) + r'(?:\b|_)'
    return bool(re.search(pattern, text))

def extract_features(text: str) -> np.ndarray:
    """Extract 256-dim feature vector using word unigrams and character trigrams with FNV-1a."""
    vec = np.zeros(FEATURE_SIZE, dtype=np.float32)
    t = text.lower()
    words = t.split()

    for i, w in enumerate(VOCABULARY):
        if is_word_match(t, w):
            vec[i] += 1.5

    offset = len(VOCABULARY)
    num_hash_slots = FEATURE_SIZE - offset
    if num_hash_slots > 0:
        for w in words:
            for j in range(len(w) - 2):
                tri = w[j:j+3]
                h = fnv1a_32(tri) % num_hash_slots
                vec[offset + h] += 0.5

    norm = np.linalg.norm(vec)
    if norm > 1e-6:
        vec /= norm
    return vec

def softmax(x: np.ndarray, temp: float = 1.0) -> np.ndarray:
    scaled = x / temp
    e_x = np.exp(scaled - np.max(scaled, axis=-1, keepdims=True))
    return e_x / np.sum(e_x, axis=-1, keepdims=True)

def sigmoid(x: np.ndarray, temp: float = 1.0) -> np.ndarray:
    scaled = x / temp
    return 1.0 / (1.0 + np.exp(-np.clip(scaled, -15.0, 15.0)))

def relu(x: np.ndarray) -> np.ndarray:
    return np.maximum(0, x)

def relu_deriv(x: np.ndarray) -> np.ndarray:
    return (x > 0).astype(np.float32)

def compute_brier_gradient(probs: np.ndarray, targets: np.ndarray) -> np.ndarray:
    """Analytical gradient of Brier score w.r.t logits for softmax."""
    diff = probs - targets
    dot = np.sum(probs * diff, axis=-1, keepdims=True)
    return 2.0 * probs * (diff - dot)

class RLCDSmallNeuralModel:
    """Multi-Layer Perceptron trained with RLCD calibration for local agent decisions."""
    def __init__(self, in_dim=256, hidden_dim=64, num_cats=9, num_roles=5, num_risks=3):
        np.random.seed(42)
        scale1 = math.sqrt(2.0 / in_dim)
        self.W1 = (np.random.randn(hidden_dim, in_dim) * scale1).astype(np.float32)
        self.b1 = np.zeros(hidden_dim, dtype=np.float32)

        scale_h = math.sqrt(2.0 / hidden_dim)
        # Category Head (Choice)
        self.W_cat = (np.random.randn(num_cats, hidden_dim) * scale_h).astype(np.float32)
        self.b_cat = np.zeros(num_cats, dtype=np.float32)

        # NeedsTools Head (Noul / Probability)
        self.W_tools = (np.random.randn(1, hidden_dim) * scale_h).astype(np.float32)
        self.b_tools = np.zeros(1, dtype=np.float32)

        # Role Head (Choice)
        self.W_role = (np.random.randn(num_roles, hidden_dim) * scale_h).astype(np.float32)
        self.b_role = np.zeros(num_roles, dtype=np.float32)

        # Safety Risk Head (Score: safe, medium, critical)
        self.W_risk = (np.random.randn(num_risks, hidden_dim) * scale_h).astype(np.float32)
        self.b_risk = np.zeros(num_risks, dtype=np.float32)

        # Subagent Delegation Head (Noul / Probability)
        self.W_delegate = (np.random.randn(1, hidden_dim) * scale_h).astype(np.float32)
        self.b_delegate = np.zeros(1, dtype=np.float32)

        # Temperature parameter for RLCD probability calibration
        self.temperature = 1.0

    def forward(self, x: np.ndarray, temp: float = 1.0):
        z1 = np.dot(x, self.W1.T) + self.b1
        h1 = relu(z1)

        logits_cat = np.dot(h1, self.W_cat.T) + self.b_cat
        prob_cat = softmax(logits_cat, temp=temp)

        logits_tools = np.dot(h1, self.W_tools.T) + self.b_tools
        prob_tools = sigmoid(logits_tools, temp=temp)

        logits_role = np.dot(h1, self.W_role.T) + self.b_role
        prob_role = softmax(logits_role, temp=temp)

        logits_risk = np.dot(h1, self.W_risk.T) + self.b_risk
        prob_risk = softmax(logits_risk, temp=temp)

        logits_del = np.dot(h1, self.W_delegate.T) + self.b_delegate
        prob_del = sigmoid(logits_del, temp=temp)

        return z1, h1, prob_cat, prob_tools, prob_role, prob_risk, prob_del

    def train_rlcd(self, X, y_cat, y_tools, y_role, y_risk, y_del, epochs=350, lr=0.006):
        """Train using RLCD proper scoring rule: Hybrid Cross-Entropy (0.7) + Brier Score (0.3)."""
        num_samples = X.shape[0]
        params = [
            self.W1, self.b1,
            self.W_cat, self.b_cat,
            self.W_tools, self.b_tools,
            self.W_role, self.b_role,
            self.W_risk, self.b_risk,
            self.W_delegate, self.b_delegate
        ]
        m = [np.zeros_like(p) for p in params]
        v = [np.zeros_like(p) for p in params]
        beta1, beta2, eps = 0.9, 0.999, 1e-8
        alpha = 0.7  # 70% CE, 30% Brier for calibrated probability alignment

        for epoch in range(1, epochs + 1):
            z1, h1, prob_cat, prob_tools, prob_role, prob_risk, prob_del = self.forward(X, temp=1.0)

            # 1. Category Gradients (Hybrid CE + Brier)
            grad_ce_cat = (prob_cat - y_cat)
            grad_brier_cat = compute_brier_gradient(prob_cat, y_cat)
            grad_logits_cat = (alpha * grad_ce_cat + (1.0 - alpha) * grad_brier_cat) / num_samples
            grad_W_cat = np.dot(grad_logits_cat.T, h1)
            grad_b_cat = np.sum(grad_logits_cat, axis=0)

            # 2. Tools Binary Gradients
            grad_logits_tools = (prob_tools - y_tools) / num_samples
            grad_W_tools = np.dot(grad_logits_tools.T, h1)
            grad_b_tools = np.sum(grad_logits_tools, axis=0)

            # 3. Role Gradients
            grad_ce_role = (prob_role - y_role)
            grad_brier_role = compute_brier_gradient(prob_role, y_role)
            grad_logits_role = (alpha * grad_ce_role + (1.0 - alpha) * grad_brier_role) / num_samples
            grad_W_role = np.dot(grad_logits_role.T, h1)
            grad_b_role = np.sum(grad_logits_role, axis=0)

            # 4. Risk Gradients
            grad_ce_risk = (prob_risk - y_risk)
            grad_brier_risk = compute_brier_gradient(prob_risk, y_risk)
            grad_logits_risk = (alpha * grad_ce_risk + (1.0 - alpha) * grad_brier_risk) / num_samples
            grad_W_risk = np.dot(grad_logits_risk.T, h1)
            grad_b_risk = np.sum(grad_logits_risk, axis=0)

            # 5. Delegation Binary Gradients
            grad_logits_del = (prob_del - y_del) / num_samples
            grad_W_del = np.dot(grad_logits_del.T, h1)
            grad_b_del = np.sum(grad_logits_del, axis=0)

            # Backprop to hidden layer
            grad_h1 = (
                np.dot(grad_logits_cat, self.W_cat) +
                np.dot(grad_logits_tools, self.W_tools) +
                np.dot(grad_logits_role, self.W_role) +
                np.dot(grad_logits_risk, self.W_risk) +
                np.dot(grad_logits_del, self.W_delegate)
            )
            grad_z1 = grad_h1 * relu_deriv(z1)
            grad_W1 = np.dot(grad_z1.T, X)
            grad_b1 = np.sum(grad_z1, axis=0)

            grads = [
                grad_W1, grad_b1,
                grad_W_cat, grad_b_cat,
                grad_W_tools, grad_b_tools,
                grad_W_role, grad_b_role,
                grad_W_risk, grad_b_risk,
                grad_W_del, grad_b_del
            ]

            # Adam step
            for i in range(len(params)):
                m[i] = beta1 * m[i] + (1 - beta1) * grads[i]
                v[i] = beta2 * v[i] + (1 - beta2) * (grads[i] ** 2)
                m_hat = m[i] / (1 - beta1 ** epoch)
                v_hat = v[i] / (1 - beta2 ** epoch)
                params[i] -= lr * m_hat / (np.sqrt(v_hat) + eps)

            if epoch % 50 == 0 or epoch == epochs:
                pred_cats = np.argmax(prob_cat, axis=1)
                true_cats = np.argmax(y_cat, axis=1)
                acc = np.mean(pred_cats == true_cats) * 100.0
                brier = np.mean(np.sum((prob_cat - y_cat) ** 2, axis=1))
                print(f"RLCD Epoch {epoch:3d}/{epochs} | Cat Acc: {acc:.1f}% | Brier Score: {brier:.4f}")

    def calibrate_temperature(self, X, y_cat):
        """Find optimal temperature T to minimize Expected Calibration Error."""
        best_t = 1.0
        best_ece = 1e9

        z1 = np.dot(X, self.W1.T) + self.b1
        h1 = relu(z1)
        logits_cat = np.dot(h1, self.W_cat.T) + self.b_cat

        for t in np.linspace(0.8, 1.8, 51):
            prob = softmax(logits_cat, temp=t)
            confs = np.max(prob, axis=1)
            preds = np.argmax(prob, axis=1)
            trues = np.argmax(y_cat, axis=1)

            # 10-bin ECE calculation
            bins = np.linspace(0.0, 1.0, 11)
            ece = 0.0
            for b in range(len(bins) - 1):
                mask = (confs >= bins[b]) & (confs < bins[b+1])
                if np.sum(mask) > 0:
                    acc_bin = np.mean(preds[mask] == trues[mask])
                    conf_bin = np.mean(confs[mask])
                    ece += (np.sum(mask) / len(X)) * abs(acc_bin - conf_bin)

            if ece < best_ece:
                best_ece = ece
                best_t = float(t)

        self.temperature = round(best_t, 3)
        print(f"🎯 Calibrated RLCD Temperature: T={self.temperature:.3f} (ECE: {best_ece:.4f})")
        return self.temperature

def export_rlcd_to_go(model: RLCDSmallNeuralModel, out_path: str):
    def format_slice_2d(mat: np.ndarray) -> str:
        rows = []
        for row in mat:
            vals = ", ".join(f"{v:.7f}" for v in row)
            rows.append(f"\t\t{{{vals}}}")
        return "{\n" + ",\n".join(rows) + ",\n\t}"

    def format_slice_1d(vec: np.ndarray) -> str:
        vals = ", ".join(f"{v:.7f}" for v in vec)
        return f"{{{vals}}}"

    vocab_str = ", ".join(f'"{w}"' for w in VOCABULARY)
    cats_str = ", ".join(f'"{c}"' for c in CATEGORIES)
    roles_str = ", ".join(f'"{r}"' for r in ROLES)
    risks_str = ", ".join(f'"{k}"' for k in RISK_LEVELS)

    code = f"""// Code generated by scripts/train_small_model.py; DO NOT EDIT.
package jev

const (
\tSmallModelInputDim     = {FEATURE_SIZE}
\tSmallModelHiddenDim    = {model.W1.shape[0]}
\tSmallModelNumCats      = {len(CATEGORIES)}
\tSmallModelNumRoles     = {len(ROLES)}
\tSmallModelNumRisks     = {len(RISK_LEVELS)}
\tSmallModelTemperature  = {model.temperature:.3f}
\tSmallModelVersion      = "small-rlcd-v2"
)

var defaultVocabulary = []string{{
\t{vocab_str},
}}

var defaultCategories = []string{{
\t{cats_str},
}}

var defaultRoles = []string{{
\t{roles_str},
}}

var defaultRisks = []string{{
\t{risks_str},
}}

var weightsW1 = [][]float32{format_slice_2d(model.W1)}

var weightsB1 = []float32{format_slice_1d(model.b1)}

var weightsWCat = [][]float32{format_slice_2d(model.W_cat)}

var weightsBCat = []float32{format_slice_1d(model.b_cat)}

var weightsWTools = []float32{format_slice_1d(model.W_tools[0])}

var weightsBTools float32 = {model.b_tools[0]:.7f}

var weightsWRole = [][]float32{format_slice_2d(model.W_role)}

var weightsBRole = []float32{format_slice_1d(model.b_role)}

var weightsWRisk = [][]float32{format_slice_2d(model.W_risk)}

var weightsBRisk = []float32{format_slice_1d(model.b_risk)}

var weightsWDelegate = []float32{format_slice_1d(model.W_delegate[0])}

var weightsBDelegate float32 = {model.b_delegate[0]:.7f}
"""
    with open(out_path, "w") as f:
        f.write(code)
    print(f"✅ Generated Go RLCD model weights: {out_path}")

def main():
    print("🧠 Preparing RLCD dataset and extracting features...")
    X = []
    y_cat = []
    y_tools = []
    y_role = []
    y_risk = []
    y_del = []

    cat_map = {c: i for i, c in enumerate(CATEGORIES)}
    role_map = {r: i for i, r in enumerate(ROLES)}
    risk_map = {k: i for i, k in enumerate(RISK_LEVELS)}

    for prompt, cat, nt, role, risk, delegate in DATASET:
        feats = extract_features(prompt)
        X.append(feats)

        cat_vec = np.zeros(len(CATEGORIES), dtype=np.float32)
        cat_vec[cat_map[cat]] = 1.0
        y_cat.append(cat_vec)

        y_tools.append([nt])

        role_vec = np.zeros(len(ROLES), dtype=np.float32)
        role_vec[role_map[role]] = 1.0
        y_role.append(role_vec)

        risk_vec = np.zeros(len(RISK_LEVELS), dtype=np.float32)
        risk_vec[risk_map[risk]] = 1.0
        y_risk.append(risk_vec)

        y_del.append([delegate])

    X = np.array(X, dtype=np.float32)
    y_cat = np.array(y_cat, dtype=np.float32)
    y_tools = np.array(y_tools, dtype=np.float32)
    y_role = np.array(y_role, dtype=np.float32)
    y_risk = np.array(y_risk, dtype=np.float32)
    y_del = np.array(y_del, dtype=np.float32)

    print(f"📊 Training RLCD small model on {len(X)} samples across 5 decision heads...")
    model = RLCDSmallNeuralModel(
        in_dim=FEATURE_SIZE,
        hidden_dim=64,
        num_cats=len(CATEGORIES),
        num_roles=len(ROLES),
        num_risks=len(RISK_LEVELS)
    )
    model.train_rlcd(X, y_cat, y_tools, y_role, y_risk, y_del, epochs=350, lr=0.007)
    model.calibrate_temperature(X, y_cat)

    # Export weights JSON
    weights_json_path = os.path.join(os.path.dirname(__file__), "..", "jev", "model_weights.json")
    weights_data = {
        "version": "small-rlcd-v2",
        "vocabulary": VOCABULARY,
        "categories": CATEGORIES,
        "roles": ROLES,
        "risk_levels": RISK_LEVELS,
        "temperature": model.temperature,
        "W1": model.W1.tolist(),
        "b1": model.b1.tolist(),
        "W_cat": model.W_cat.tolist(),
        "b_cat": model.b_cat.tolist(),
        "W_tools": model.W_tools.tolist(),
        "b_tools": model.b_tools.tolist(),
        "W_role": model.W_role.tolist(),
        "b_role": model.b_role.tolist(),
        "W_risk": model.W_risk.tolist(),
        "b_risk": model.b_risk.tolist(),
        "W_delegate": model.W_delegate.tolist(),
        "b_delegate": model.b_delegate.tolist()
    }
    with open(weights_json_path, "w") as f:
        json.dump(weights_data, f)
    print(f"✅ Exported JSON weights: {weights_json_path}")

    # Export Go source
    go_path = os.path.join(os.path.dirname(__file__), "..", "jev", "weights_generated.go")
    export_rlcd_to_go(model, go_path)

if __name__ == "__main__":
    main()
