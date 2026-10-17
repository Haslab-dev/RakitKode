#!/usr/bin/env python3
"""
train_small_model.py
Trains a lightweight Neural Network (MLP) for RakitKode's local smart decision engine.
This small model replaces external API calls to Jev when the Jev API key is missing
or Jev returns an error.

Features:
- Subword & word token feature mapping (256-dim feature vector)
- Multi-Layer Perceptron (256 -> 64 -> Category/Role/NeedsTools heads)
- Trained with Adam optimizer and Cross-Entropy loss
- Exports model weights directly to Go source code: jev/weights_generated.go
"""

import json
import math
import os
import sys
import numpy as np

# Categories
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

# Roles
ROLES = [
    "coder",       # 0
    "researcher",  # 1
    "reviewer",    # 2
    "debugger",    # 3
    "devops"       # 4
]

# Feature Vocabulary: keywords, command fragments, and semantic stems
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
    "http", "fetch", "url", "web", "download", "scrape", "browse", "api request", "curl", "endpoint",
    "website", "html", "documentation link", "online",
    # Skills / Plugins
    "skill", "learn", "how to", "install skill", "use skill", "plugin", "workflow", "recipe",
    # MCP
    "mcp", "server", "tool protocol", "call mcp", "database query", "sqlite", "postgres",
    # Conversational / none
    "hello", "hi", "hey", "who are you", "what can you do", "thanks", "thank you", "bye",
    "explain", "summarize", "tell me about", "what is", "why", "how does", "help",
    # Programming concepts
    "function", "class", "struct", "interface", "method", "variable", "package", "import",
    "type", "error", "nil", "null", "pointer", "goroutine", "channel", "thread", "memory",
    "leak", "crash", "panic", "segfault", "benchmark", "optimize", "slow", "performance",
    # Safety keywords
    "rm -rf", "delete all", "drop table", "truncate", "format disk", "sudo", "chmod 777",
    "api_key", "secret", "password", "token", "env", "credentials", "id_rsa", "auth"
]

# Build vocabulary index up to 256 dimensions
FEATURE_SIZE = 256
vocab_map = {word: i for i, word in enumerate(VOCABULARY)}

# Training dataset: (prompt, category, needs_tools, role)
DATASET = [
    # Explore
    ("find all files matching *.go in the repository", "explore", 1.0, "researcher"),
    ("where is the entrypoint defined in this project?", "explore", 1.0, "researcher"),
    ("list all files in internal/gateway directory", "explore", 1.0, "researcher"),
    ("search for function ExecuteTurnWithModel across the codebase", "explore", 1.0, "researcher"),
    ("locate all places where Jev router is instantiated", "explore", 1.0, "researcher"),
    ("show me the project directory tree and structure", "explore", 1.0, "researcher"),
    ("grep for error handling in agent engine", "explore", 1.0, "researcher"),
    ("find where the config is loaded", "explore", 1.0, "researcher"),
    ("investigate and audit the codebase for potential security leaks", "explore", 1.0, "researcher"),
    ("research and trace symbols across the project", "explore", 1.0, "researcher"),
    ("audit codebase for vulnerabilities and security leaks", "explore", 1.0, "researcher"),

    # Read
    ("read the contents of main.go", "read", 1.0, "coder"),
    ("view file internal/gateway/subagent.go line 1 to 50", "read", 1.0, "coder"),
    ("show me what is inside package.json", "read", 1.0, "coder"),
    ("check the implementation of Router in jev/jev.go", "read", 1.0, "coder"),
    ("display the README.md file", "read", 1.0, "researcher"),
    ("examine the go.mod dependencies", "read", 1.0, "coder"),
    ("cat the error log file", "read", 1.0, "debugger"),
    ("open and inspect internal/config/config.go", "read", 1.0, "coder"),

    # Modify
    ("edit main.go to update version string to v0.2.8", "modify", 1.0, "coder"),
    ("write a new unit test in jev/local_test.go", "modify", 1.0, "coder"),
    ("refactor the subagent router to handle nil checks", "modify", 1.0, "coder"),
    ("fix bug in turn execution loop where context cancelled was ignored", "modify", 1.0, "debugger"),
    ("create a new file scripts/deploy.sh", "modify", 1.0, "devops"),
    ("patch the memory leak in buffer pool", "modify", 1.0, "debugger"),
    ("add a new method to Router struct", "modify", 1.0, "coder"),
    ("replace deprecated function with the new API", "modify", 1.0, "coder"),
    ("implement token estimation calculator", "modify", 1.0, "coder"),

    # Execute
    ("run go test ./... to check all unit tests", "execute", 1.0, "devops"),
    ("build the binary with go build ./cmd/rakit", "execute", 1.0, "devops"),
    ("execute npm run build", "execute", 1.0, "devops"),
    ("compile the wails app", "execute", 1.0, "devops"),
    ("run the benchmarks on model inference", "execute", 1.0, "devops"),
    ("execute bash command ls -la", "execute", 1.0, "devops"),
    ("start the local test server on port 8080", "execute", 1.0, "devops"),
    ("run linter oxlint and check code style", "execute", 1.0, "reviewer"),

    # Git
    ("git commit -m 'feat: update version' and push to origin", "git", 1.0, "devops"),
    ("show git status and git diff", "git", 1.0, "reviewer"),
    ("create a new git branch feat/small-model", "git", 1.0, "devops"),
    ("check git log for the last 5 commits", "git", 1.0, "reviewer"),
    ("tag this release with v0.2.8 and push tags", "git", 1.0, "devops"),
    ("git checkout main and pull latest changes", "git", 1.0, "devops"),
    ("stash current changes in git", "git", 1.0, "coder"),

    # Web
    ("fetch documentation from https://golang.org/doc", "web", 1.0, "researcher"),
    ("curl the API endpoint and inspect response", "web", 1.0, "researcher"),
    ("download the latest release assets from github", "web", 1.0, "devops"),
    ("browse the web to research Grok bot architecture", "web", 1.0, "researcher"),
    ("scrape the release notes page", "web", 1.0, "researcher"),

    # Skills
    ("install skill for impeccble UI design", "skills", 1.0, "coder"),
    ("how do I create a new custom skill?", "skills", 0.0, "coder"),
    ("show me available skills in the catalog", "skills", 1.0, "researcher"),

    # MCP
    ("query postgres database via mcp server", "mcp", 1.0, "coder"),
    ("call mcp tool filesystem list_allowed_directories", "mcp", 1.0, "devops"),
    ("connect to mcp sqlite server and inspect schema", "mcp", 1.0, "coder"),

    # None / Conversational / Reasoning
    ("hello, who are you and how can you help me?", "none", 0.0, "researcher"),
    ("hello, how are you today?", "none", 0.0, "coder"),
    ("hello how are you", "none", 0.0, "coder"),
    ("hi how are you doing?", "none", 0.0, "coder"),
    ("good morning, how are you today?", "none", 0.0, "coder"),
    ("hey assistant, what can you do?", "none", 0.0, "coder"),
    ("tell me a joke", "none", 0.0, "coder"),
    ("explain the difference between concurrency and parallelism", "none", 0.0, "coder"),
    ("what is the time complexity of quicksort?", "none", 0.0, "coder"),
    ("summarize the pros and cons of microservices", "none", 0.0, "reviewer"),
    ("thank you for your help!", "none", 0.0, "researcher"),
    ("why does Go use goroutines instead of OS threads?", "none", 0.0, "coder"),
    ("review this code architecture conceptually and give suggestions", "none", 0.0, "reviewer")
]

# Additional synthetic variations to broaden training
for base_text, cat, nt, role in list(DATASET):
    DATASET.append(("please " + base_text, cat, nt, role))
    DATASET.append(("can you " + base_text + " now?", cat, nt, role))
    DATASET.append(("I need you to " + base_text, cat, nt, role))

import re

def is_word_match(text: str, word: str) -> bool:
    if " " in word:
        return word in text
    pattern = r'(?:\b|_)' + re.escape(word) + r'(?:\b|_)'
    return bool(re.search(pattern, text))

def extract_features(text: str) -> np.ndarray:
    """Extract 256-dim feature vector using word unigrams and character trigrams."""
    vec = np.zeros(FEATURE_SIZE, dtype=np.float32)
    t = text.lower()
    words = t.split()

    # Exact vocabulary matching with word boundaries
    for i, w in enumerate(VOCABULARY):
        if is_word_match(t, w):
            vec[i] += 1.5

    # Character n-gram hashing into remaining slots (len(VOCABULARY) -> FEATURE_SIZE)
    offset = len(VOCABULARY)
    num_hash_slots = FEATURE_SIZE - offset
    if num_hash_slots > 0:
        for w in words:
            for j in range(len(w) - 2):
                tri = w[j:j+3]
                h = abs(hash(tri)) % num_hash_slots
                vec[offset + h] += 0.5

    # L2 normalize
    norm = np.linalg.norm(vec)
    if norm > 1e-6:
        vec /= norm
    return vec

def softmax(x: np.ndarray) -> np.ndarray:
    e_x = np.exp(x - np.max(x, axis=-1, keepdims=True))
    return e_x / np.sum(e_x, axis=-1, keepdims=True)

def sigmoid(x: np.ndarray) -> np.ndarray:
    return 1.0 / (1.0 + np.exp(-np.clip(x, -15.0, 15.0)))

def relu(x: np.ndarray) -> np.ndarray:
    return np.maximum(0, x)

def relu_deriv(x: np.ndarray) -> np.ndarray:
    return (x > 0).astype(np.float32)

class SmallNeuralModel:
    """Multi-Layer Perceptron trained for local agent routing."""
    def __init__(self, in_dim=256, hidden_dim=64, num_cats=9, num_roles=5):
        np.random.seed(42)
        # Hidden layer
        scale1 = math.sqrt(2.0 / in_dim)
        self.W1 = (np.random.randn(hidden_dim, in_dim) * scale1).astype(np.float32)
        self.b1 = np.zeros(hidden_dim, dtype=np.float32)

        # Category head
        scale_cat = math.sqrt(2.0 / hidden_dim)
        self.W_cat = (np.random.randn(num_cats, hidden_dim) * scale_cat).astype(np.float32)
        self.b_cat = np.zeros(num_cats, dtype=np.float32)

        # NeedsTools head
        self.W_tools = (np.random.randn(1, hidden_dim) * scale_cat).astype(np.float32)
        self.b_tools = np.zeros(1, dtype=np.float32)

        # Role head
        self.W_role = (np.random.randn(num_roles, hidden_dim) * scale_cat).astype(np.float32)
        self.b_role = np.zeros(num_roles, dtype=np.float32)

    def forward(self, x: np.ndarray):
        # x: [batch, in_dim]
        z1 = np.dot(x, self.W1.T) + self.b1
        h1 = relu(z1)

        logits_cat = np.dot(h1, self.W_cat.T) + self.b_cat
        prob_cat = softmax(logits_cat)

        logits_tools = np.dot(h1, self.W_tools.T) + self.b_tools
        prob_tools = sigmoid(logits_tools)

        logits_role = np.dot(h1, self.W_role.T) + self.b_role
        prob_role = softmax(logits_role)

        return z1, h1, prob_cat, prob_tools, prob_role

    def train(self, X: np.ndarray, y_cat: np.ndarray, y_tools: np.ndarray, y_role: np.ndarray, epochs=300, lr=0.005):
        num_samples = X.shape[0]
        # Adam optimizer state
        params = [self.W1, self.b1, self.W_cat, self.b_cat, self.W_tools, self.b_tools, self.W_role, self.b_role]
        m = [np.zeros_like(p) for p in params]
        v = [np.zeros_like(p) for p in params]
        beta1, beta2, eps = 0.9, 0.999, 1e-8

        for epoch in range(1, epochs + 1):
            z1, h1, prob_cat, prob_tools, prob_role = self.forward(X)

            # Gradients
            # Category cross-entropy
            grad_logits_cat = (prob_cat - y_cat) / num_samples
            grad_W_cat = np.dot(grad_logits_cat.T, h1)
            grad_b_cat = np.sum(grad_logits_cat, axis=0)

            # Tools binary cross-entropy
            grad_logits_tools = (prob_tools - y_tools) / num_samples
            grad_W_tools = np.dot(grad_logits_tools.T, h1)
            grad_b_tools = np.sum(grad_logits_tools, axis=0)

            # Role cross-entropy
            grad_logits_role = (prob_role - y_role) / num_samples
            grad_W_role = np.dot(grad_logits_role.T, h1)
            grad_b_role = np.sum(grad_logits_role, axis=0)

            # Backprop to hidden
            grad_h1 = (np.dot(grad_logits_cat, self.W_cat) +
                       np.dot(grad_logits_tools, self.W_tools) +
                       np.dot(grad_logits_role, self.W_role))
            grad_z1 = grad_h1 * relu_deriv(z1)

            grad_W1 = np.dot(grad_z1.T, X)
            grad_b1 = np.sum(grad_z1, axis=0)

            grads = [grad_W1, grad_b1, grad_W_cat, grad_b_cat, grad_W_tools, grad_b_tools, grad_W_role, grad_b_role]

            # Adam update
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
                cat_loss = -np.mean(np.sum(y_cat * np.log(prob_cat + 1e-12), axis=1))
                print(f"Epoch {epoch:3d}/{epochs} | Loss: {cat_loss:.4f} | Accuracy: {acc:.1f}%")

def export_to_go(model: SmallNeuralModel, out_path: str):
    """Exports model weights directly into Go source code for zero-dependency inference."""
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
    w1_str = format_slice_2d(model.W1)
    b1_str = format_slice_1d(model.b1)
    wcat_str = format_slice_2d(model.W_cat)
    bcat_str = format_slice_1d(model.b_cat)
    wtools_str = format_slice_1d(model.W_tools[0])
    btools_val = f"{model.b_tools[0]:.7f}"
    wrole_str = format_slice_2d(model.W_role)
    brole_str = format_slice_1d(model.b_role)

    code = f"""// Code generated by scripts/train_small_model.py; DO NOT EDIT.
package jev

const (
\tSmallModelInputDim  = {FEATURE_SIZE}
\tSmallModelHiddenDim = {model.W1.shape[0]}
\tSmallModelNumCats   = {len(CATEGORIES)}
\tSmallModelNumRoles  = {len(ROLES)}
\tSmallModelVersion   = "small-nn-v1"
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

var weightsW1 = [][]float32{w1_str}

var weightsB1 = []float32{b1_str}

var weightsWCat = [][]float32{wcat_str}

var weightsBCat = []float32{bcat_str}

var weightsWTools = []float32{wtools_str}

var weightsBTools float32 = {btools_val}

var weightsWRole = [][]float32{wrole_str}

var weightsBRole = []float32{brole_str}
"""
    with open(out_path, "w") as f:
        f.write(code)
    print(f"✅ Generated Go model weights: {out_path}")
    with open(out_path, "w") as f:
        f.write(code)
    print(f"✅ Generated Go model weights: {out_path}")

def main():
    print("🧠 Preparing dataset and extracting features...")
    X = []
    y_cat = []
    y_tools = []
    y_role = []

    cat_map = {c: i for i, c in enumerate(CATEGORIES)}
    role_map = {r: i for i, r in enumerate(ROLES)}

    for prompt, cat, nt, role in DATASET:
        feats = extract_features(prompt)
        X.append(feats)

        cat_vec = np.zeros(len(CATEGORIES), dtype=np.float32)
        cat_vec[cat_map[cat]] = 1.0
        y_cat.append(cat_vec)

        y_tools.append([nt])

        role_vec = np.zeros(len(ROLES), dtype=np.float32)
        role_vec[role_map[role]] = 1.0
        y_role.append(role_vec)

    X = np.array(X, dtype=np.float32)
    y_cat = np.array(y_cat, dtype=np.float32)
    y_tools = np.array(y_tools, dtype=np.float32)
    y_role = np.array(y_role, dtype=np.float32)

    print(f"📊 Training small model on {len(X)} samples across {len(CATEGORIES)} categories...")
    model = SmallNeuralModel(in_dim=FEATURE_SIZE, hidden_dim=64, num_cats=len(CATEGORIES), num_roles=len(ROLES))
    model.train(X, y_cat, y_tools, y_role, epochs=300, lr=0.008)

    # Export weights JSON
    weights_json_path = os.path.join(os.path.dirname(__file__), "..", "jev", "model_weights.json")
    weights_data = {
        "version": "small-nn-v1",
        "vocabulary": VOCABULARY,
        "categories": CATEGORIES,
        "roles": ROLES,
        "W1": model.W1.tolist(),
        "b1": model.b1.tolist(),
        "W_cat": model.W_cat.tolist(),
        "b_cat": model.b_cat.tolist(),
        "W_tools": model.W_tools.tolist(),
        "b_tools": model.b_tools.tolist(),
        "W_role": model.W_role.tolist(),
        "b_role": model.b_role.tolist()
    }
    with open(weights_json_path, "w") as f:
        json.dump(weights_data, f)
    print(f"✅ Exported JSON weights: {weights_json_path}")

    # Export Go code
    go_path = os.path.join(os.path.dirname(__file__), "..", "jev", "weights_generated.go")
    export_to_go(model, go_path)

if __name__ == "__main__":
    main()
