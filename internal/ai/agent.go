package ai

import (
	"fmt"
	"strings"

	"code-review/internal/change"
	"code-review/internal/findings"
)

func SelectAgents(ids []string) ([]ReviewAgent, error) {
	available := make(map[AgentID]ReviewAgent)
	for _, agent := range DefaultAgents() {
		available[agent.ID] = agent
	}
	selected := make([]ReviewAgent, 0, len(ids))
	seen := make(map[AgentID]struct{}, len(ids))
	for _, value := range ids {
		id := AgentID(strings.TrimSpace(value))
		agent, exists := available[id]
		if !exists {
			return nil, fmt.Errorf("unsupported AI review agent %q", value)
		}
		if _, duplicate := seen[id]; duplicate {
			return nil, fmt.Errorf("duplicate AI review agent %q", value)
		}
		seen[id] = struct{}{}
		selected = append(selected, agent)
	}
	if len(selected) == 0 {
		return nil, fmt.Errorf("at least one AI review agent is required")
	}
	return selected, nil
}

type AgentID string

const (
	AgentCorrectness    AgentID = "correctness"
	AgentSecurity       AgentID = "security"
	AgentSecurityTriage AgentID = "security-triage"
)

type ReviewAgent struct {
	ID           AgentID
	Instructions string
	Categories   map[findings.Category]struct{}
}

const agentBaseInstructions = `Review only issues introduced by these staged changes. Report findings on changed lines with evidence from the supplied diff. Do not invent runtime behavior, missing context, or unrelated legacy issues. Return structured findings only. Set proposedFix only when you can provide an exact replacement for the finding's complete added-line range; use replacement text without diff prefixes, or null when an exact safe fix is not possible.`

// securityChecklist is the extensible data-driven security coverage the
// security agent is required to evaluate. Routing and agents both read from it,
// so adding a class of vulnerability here improves both review and triggering.
const securityChecklist = `Evaluate the security of the changed code across ALL of the following coverage classes, not only the ones you find familiar:
- Injection: SQL/NoSQL, OS command, LDAP, XPath, template (SSTI), XML, and email header injection; unsafe string interpolation into queries, commands, or constructed files.
- Authentication and identity: broken/missing authentication and authorization, default or enum bruitable credentials, insecure password storage or hashing, session fixation and hijacking, cookie configuration (secure, httpOnly, SameSite), logout and invalidation.
- Authorization and access control: broken object-level or function-level authorization (IDOR/BOLA/BFLA), missing ownership checks, permission and role escalation, path traversal and arbitrary file access beyond intended roots.
- Sensitive data exposure: hardcoded secrets, credentials, tokens, keys, PII in logs, exceptions, query strings, or responses; insufficient encryption for stored or in-transit sensitive data; random-number or IV/nonce reuse.
- Cryptography: weak or obsolete algorithms (MD5, SHA1, DES, ECB), unsafe randomness, weak key sizes, hardcoded keys, mislabeled initialization vectors, padding oracles.
- Server-side risks: SSRF (user-controlled fetch/dial/redirect targets), XML external entity (XXE) expansion, insecure deserialization (yaml.load, pickle, unmarshal of untrusted data), unsafe redirects, header injection/CRLF, request smuggling, and insufficient output encoding.
- Client-side: reflected, stored, and DOM-based XSS; unsafe innerHTML/text injection; CSP and scripting-free rendering; open redirects.
- Sensitive actions: CSRF protection on mutating endpoints, unsafe command and subprocess execution, unsafe file reads/writes/stat/deletion, symlink/TOCTOU races, and world-writable permissions.
- Availability & abuse: unbounded memory/files, catastrophic regular expressions (ReDoS), missing rate limiting or quotas on sensitive endpoints, and lockout.

Report a security finding only when a changed line directly introduces or worsens one of these risks, with evidence from the diff.`

func DefaultAgents() []ReviewAgent {
	return []ReviewAgent{
		{
			ID: AgentCorrectness,
			Instructions: agentBaseInstructions + " Act as the correctness specialist. Focus only on edge cases, nil/null handling, error handling, resource cleanup, transactions, concurrency, and incorrect assumptions. " +
				"Use the correctness category for concrete defects and quality only when the issue is directly correctness-adjacent.",
			Categories: map[findings.Category]struct{}{
				findings.CategoryCorrectness: {},
				findings.CategoryQuality:     {},
			},
		},
		{
			ID: AgentSecurity,
			Instructions: agentBaseInstructions + " Act as the security specialist. " + securityChecklist +
				" Return only security-category findings supported by changed-line evidence.",
			Categories: map[findings.Category]struct{}{
				findings.CategorySecurity: {},
			},
		},
	}
}

// securityTriageInstructions drive the lightweight classifier that always
// evaluates the redacted diff before deep security review is considered.
const securityTriageInstructions = `You are a security triage classifier for staged code changes. Decide ONLY whether the supplied redacted diff plausibly introduces or expands an attack or abuse surface that warrants a deep security review. Consider authentication and authorization, credentials or secret handling, injection, command or process execution, network calls and SSRF, file or path handling, deserialization, cryptography, sensitive data exposure, permission changes, and missing rate limits or abuse controls. Answer escalate=true whenever such a surface is plausible or you are uncertain; answer escalate=false only when the change is clearly unrelated to security. Never report findings.`

// securityTriageAgent is the internal cheap gate in front of the deep
// security specialist. It produces a TriageResponse, not findings.
func securityTriageAgent() ReviewAgent {
	return ReviewAgent{
		ID:           AgentSecurityTriage,
		Instructions: securityTriageInstructions,
		Categories:   map[findings.Category]struct{}{},
	}
}

// RequiresSecurityReview is the deterministic half of the deep-security gate:
// runDeepSecurity = RequiresSecurityReview(changes) || securityTriage.Escalate.
func RequiresSecurityReview(changes change.ChangeSet) bool {
	for _, file := range changes.Files {
		path := file.Path()
		if containsSecurityTerm(path) || isSecuritySensitivePath(path) {
			return true
		}
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind == change.LineAdded && containsSecurityTerm(line.Content) {
					return true
				}
			}
		}
	}
	return false
}

// isSecuritySensitivePath flags credential, configuration, and dependency
// files whose addition is security-relevant even without a code keyword.
func isSecuritySensitivePath(path string) bool {
	lower := strings.ToLower(path)
	if strings.HasPrefix(lower, ".env") || strings.Contains(lower, "/.env") {
		return true
	}
	for _, suffix := range []string{
		".env", ".npmrc", ".pypirc", ".netrc", ".aws", ".ssh", ".kubeconfig",
		".pem", ".key", ".p12", ".pfx", ".jks", "config.yaml", "config.yml",
		"dockerfile", "containerfile", "credentials", "credential",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	for _, marker := range []string{
		"secret", "credential", "token", "password", "session", "id_rsa", "jwt",
	} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

// containsSecurityTerm reports security-relevant surfaces in an added line or
// path. A conservative match is intentional: the security agent reports only
// findings supported by changed-line evidence, so over-triggering costs tokens
// while under-triggering silently loses security coverage.
func containsSecurityTerm(value string) bool {
	value = strings.ToLower(value)
	for _, term := range []string{
		// Injection and dynamic execution surfaces.
		"api_key", "apikey", "auth", "command", "cookie", "credential", "crypto", "decrypt", "encrypt",
		"header", "jwt", "oauth", "passwd", "password", "permission", "private_key",
		"request", "secret", "security", "session", "shell", "sql", "token", "upload",
		"exec.", "exec(", "exec.command", "os/exec", "system(", "eval(", "subprocess",
		"query", "orm", "raw", "ldap", "xpath", "template", "sprintf", "fprintf", "reflect",
		"unmarshal", "marshal", "deserialize", "unserialize", "pickle", "yaml",
		// Identity, sessions, and access control.
		"authenticate", "authoriz", "role", "acl", "login", "logon", "logout", "signin", "signup",
		"username", "userid", "csrf", "cors", "origin", "keyring",
		// Sensitive data and logging.
		"anonym", "pii", "privacy", "redact", "logger", "logging",
		// Cryptography.
		"hash", "hmac", "md5", "sha1", "sha256", "aes", "rsa", "cipher", "nonce",
		"random", "rand.", "entropy", "pem", "pgp",
		// File, path, and system integrity.
		"download", "path", "readdir", "unlink", "removefile", "chmod", "chown",
		"symlink", "readlink", "realpath", "tmpdir", "tempfile", "zip", "unzip",
		"archive", "mkdir", "fs.",
		// Network and client-side.
		"redirect", "iframe", "innerhtml", "document.", "querystring", "http.", "https.",
		"url.", "fetch", "curl", "net.", "dial", "listen", "smtp",
		// Availability and abuse.
		"ratelimit", "throttle", "quota", "lockout", "brute", "captcha", "regex",
		// Misc security boundaries.
		"sudo", "privileges", "proc.", "/proc", "environ", "getenv", "admin",
	} {
		if strings.Contains(value, term) {
			return true
		}
	}
	return false
}
