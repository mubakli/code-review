package ai

import (
	stdcontext "context"
	"fmt"
	"strings"

	"code-review/internal/ai/context"
	"code-review/internal/change"
	"code-review/internal/findings"
)

// AgentRole declares how an agent participates in review. Analyzers produce
// findings; routers produce routing decisions only.
type AgentRole string

const (
	RoleAnalyzer AgentRole = "analyzer"
	RoleRouter   AgentRole = "router"
)

// AgentSpec declaratively describes one specialist agent. Behavior is split
// into a role (what the agent produces) and a routing policy (when it runs).
type AgentSpec struct {
	ID          AgentID
	Description string
	Prompt      string
	Role        AgentRole
	Policy      RoutingPolicy
	Categories  map[findings.Category]struct{}
}

// RoutingPolicy decides whether an agent runs for a change set, and with
// which related context. Policies may consult router agents through the scope
// and record their activity before returning a Decision.
type RoutingPolicy interface {
	ShouldRun(ctx stdcontext.Context, changes change.ChangeSet, staticFindings []findings.Finding, result *ReviewResult, batchIndex *int, scope PolicyScope) (Decision, error)
}

// Decision is the declarative output of a routing policy.
type Decision struct {
	Run     bool
	Context []context.ContextFile
}

// PolicyScope is the capability surface policies may use to route and to
// resolve related staged context for a deep specialist review.
type PolicyScope interface {
	RunRouter(ctx stdcontext.Context, spec AgentSpec, changes change.ChangeSet, staticFindings []findings.Finding, result *ReviewResult, batchIndex *int) (bool, error)
	ResolveStagedContext(ctx stdcontext.Context, changes change.ChangeSet) ([]context.ContextFile, error)
}

// AlwaysRun routes every eligible change to its agent.
type AlwaysRun struct{}

func (AlwaysRun) ShouldRun(stdcontext.Context, change.ChangeSet, []findings.Finding, *ReviewResult, *int, PolicyScope) (Decision, error) {
	return Decision{Run: true}, nil
}

// SecurityEscalationPolicy is the fail-closed gate in front of the deep
// security specialist: runDeepSecurity = deterministic signal || triage-escalate.
// Deterministic signals skip the router; otherwise the triage router decides,
// and any triage error escalates.
type SecurityEscalationPolicy struct {
	// Router is the triage router consulted before escalation. A zero value
	// falls back to SecurityTriageRouter.
	Router AgentSpec
}

func (p SecurityEscalationPolicy) ShouldRun(ctx stdcontext.Context, changes change.ChangeSet, staticFindings []findings.Finding, result *ReviewResult, batchIndex *int, scope PolicyScope) (Decision, error) {
	if RequiresSecurityReview(changes) {
		return Decision{Run: true}, nil
	}
	router := p.Router
	if router.ID == "" {
		router = SecurityTriageRouter
	}
	escalate, err := scope.RunRouter(ctx, router, changes, staticFindings, result, batchIndex)
	if err != nil {
		return Decision{}, err
	}
	if !escalate {
		return Decision{}, nil
	}
	contextFiles, err := scope.ResolveStagedContext(ctx, changes)
	if err != nil {
		contextFiles = nil // related context is advisory; the diff review proceeds
	}
	return Decision{Run: true, Context: contextFiles}, nil
}

func SelectAgents(ids []string) ([]AgentSpec, error) {
	available := make(map[AgentID]AgentSpec)
	for _, agent := range DefaultAgents() {
		available[agent.ID] = agent
	}
	selected := make([]AgentSpec, 0, len(ids))
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

const agentBasePrompt = `Review only the issues introduced by these staged changes. Report findings on changed lines with evidence from the supplied diff. Do not invent runtime behavior, missing context, or unrelated legacy issues. Return structured findings only. Set proposedFix only when you can provide an exact replacement for the finding's complete added-line range; use replacement text without diff prefixes, or null when an exact safe fix is not possible.`

// securityChecklist is the extensible data-driven security coverage the
// security agent is required to evaluate. Routing and agents both read from it,
// so adding a class of vulnerability here improves both review and triggering.
const securityChecklist = `Evaluate the security of the changed code across ALL of the following coverage classes, not only the ones you find familiar:
- Injection: SQL/NoSQL, OS command, LDAP, XPath, template (SSTI), XML, and email header injection; unsafe string interpolation into queries, commands, or constructed files.
- Authentication and identity: broken/missing authentication and authorization, default or brute-forceable credentials, insecure password storage or hashing, session fixation and hijacking, cookie configuration (secure, httpOnly, SameSite), logout and invalidation.
- Authorization and access control: broken object-level or function-level authorization (IDOR/BOLA/BFLA), missing ownership checks, permission and role escalation, path traversal and arbitrary file access beyond intended roots.
- Sensitive data exposure: hardcoded secrets, credentials, tokens, keys, PII in logs, exceptions, query strings, or responses; insufficient encryption for stored or in-transit sensitive data; random-number or IV/nonce reuse.
- Cryptography: weak or obsolete algorithms (MD5, SHA1, DES, ECB), unsafe randomness, weak key sizes, hardcoded keys, mislabeled initialization vectors, padding oracles.
- Server-side risks: SSRF (user-controlled fetch/dial/redirect targets), XML external entity (XXE) expansion, insecure deserialization (yaml.load, pickle, unmarshal of untrusted data), unsafe redirects, header injection/CRLF, request smuggling, and insufficient output encoding.
- Client-side: reflected, stored, and DOM-based XSS; unsafe innerHTML/text injection; CSP and scripting-free rendering; open redirects.
- Sensitive actions: CSRF protection on mutating endpoints, unsafe command and subprocess execution, unsafe file reads/writes/stat/deletion, symlink/TOCTOU races, and world-writable permissions.
- Availability & abuse: unbounded memory/files, catastrophic regular expressions (ReDoS), missing rate limiting or quotas on sensitive endpoints, and lockout.

Report a security finding only when a changed line directly introduces or worsens one of these risks, with evidence from the diff.`

// DefaultAgents are the user-selectable specialist agents. The triage router
// is not listed because it is consulted by SecurityEscalationPolicy instead.
func DefaultAgents() []AgentSpec {
	return []AgentSpec{
		{
			ID:          AgentCorrectness,
			Description: "Correctness specialist for edge cases, error handling, concurrency, and resource lifecycles",
			Prompt: agentBasePrompt + " Act as the correctness specialist. Focus only on edge cases, nil/null handling, error handling, resource cleanup, transactions, concurrency, and incorrect assumptions. " +
				"Use the correctness category for concrete defects and quality only when the issue is directly correctness-adjacent.",
			Role:   RoleAnalyzer,
			Policy: AlwaysRun{},
			Categories: map[findings.Category]struct{}{
				findings.CategoryCorrectness: {},
				findings.CategoryQuality:     {},
			},
		},
		{
			ID:          AgentSecurity,
			Description: "Security specialist for attack and abuse surfaces in changed code",
			Prompt: agentBasePrompt + " Act as the security specialist. " + securityChecklist +
				" Report only security-category findings supported by changed-line evidence.",
			Role:   RoleAnalyzer,
			Policy: SecurityEscalationPolicy{},
			Categories: map[findings.Category]struct{}{
				findings.CategorySecurity: {},
			},
		},
	}
}

// securityTriagePrompt drives the lightweight router that decides whether the
// redacted diff warrants deep security review. Triage routes; it never
// diagnoses: it describes what the deep security agent must examine, because
// enforcement (e.g. an authorization middleware) can live outside the diff.
const securityTriagePrompt = `You are a security triage router for staged code changes. Your job is routing, not diagnosis: you decide whether a diff warrants deep security review and describe WHAT to examine, never WHAT is wrong.

Never conclude that a vulnerability exists or is absent. Do not say "SQL injection present", "IDOR confirmed", "open redirect", or any other finding-style claim. The diff is redacted and the surrounding system is invisible: the authorization middleware, validators, frameworks, and call sites that enforce or mitigate a risk live elsewhere. A surface visible in this diff may already be enforced outside it; only the deep security agent can confirm or dismiss that.

Identify whether this change plausibly touches an attack or abuse surface, and describe each surface as an area the deep security agent must examine:
- authentication and authorization (identity checks, ownership, object-level access, privilege boundaries)
- credentials and secret handling
- injection surfaces and dynamic construction (queries, commands, templates, serialization)
- command or process execution
- network calls, SSRF, and redirects
- file and path handling, archive extraction, traversal
- deserialization of untrusted data
- cryptography and randomness
- sensitive data exposure, logging, and transport
- permission changes and privileged operations
- missing rate limits or abuse controls

In "surfaces" list only what the deep agent must inspect, phrased as observables in this diff (a data flow, a control-flow choice, a changed boundary) — "user-controlled input reaches a database query built by string concatenation", not "SQL injection exists".
In "rationale" state why the deep analysis is warranted in at most two sentences; when enforcement is absent from the diff, say the surface awaits confirmation against the full codebase.

Answer escalate=true whenever such a surface is plausible — including when the pattern is uncertain or the enforcement could live outside this diff; answer escalate=false only when the change is clearly unrelated to security. Never report findings, and never make claims that require files outside this diff.`

// SecurityTriageRouter is the declarative router in front of the deep security
// specialist. It produces routing decisions (escalate), never findings.
var SecurityTriageRouter = AgentSpec{
	ID:          AgentSecurityTriage,
	Description: "Triage router that routes changes for deep security review without diagnosing findings",
	Prompt:      securityTriagePrompt,
	Role:        RoleRouter,
	Policy:      AlwaysRun{},
}

// RequiresSecurityReview is the deterministic half of the SecurityEscalationPolicy
// gate: runDeepSecurity = RequiresSecurityReview(changes) || triage-escalate.
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
		"api_key", "apikey", "auth", "command", "cookie", "credential", "crypto", "1desdecrypt", "encrypt",
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
