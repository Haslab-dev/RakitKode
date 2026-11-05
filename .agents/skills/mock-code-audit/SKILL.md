---
name: mock-code-audit
description: Security and code quality audit playbook for Go and Python services
when_to_use: When reviewing code quality, security vulnerabilities, or concurrency safety
---

# Mock Code Audit Playbook

Automated inspection checklist for source code:
1. Check for hardcoded secrets or API tokens.
2. Verify goroutine leaks and context cancellations.
3. Check SQL injection and shell escape sanitization.
4. Run static analysis tools in `scripts/audit_check.sh`.
