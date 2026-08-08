package context

import (
	"regexp"
	"sort"
	"strings"

	"code-review/internal/change"
)

// maxDiffSymbols bounds how many candidate symbols flow into one context
// request, so a single large diff cannot flood the resolver.
const maxDiffSymbols = 12

var (
	identifierPattern = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]{2,}`)
	stringLiteral     = regexp.MustCompile(`"([^"]*)"`)
)

// commonWords are tokens too generic to resolve related context against.
// Symbols for surrounding code are useful when distinctive (FindUser,
// ownershipPolicy), not when they match half the repository (request, id).
var commonWords = map[string]struct{}{
	"package": {}, "import": {}, "func": {}, "funcs": {}, "fn": {}, "var": {},
	"const": {}, "type": {}, "struct": {}, "interface": {}, "map": {}, "chan": {},
	"range": {}, "return": {}, "if": {}, "else": {}, "elif": {}, "for": {}, "while": {},
	"break": {}, "continue": {}, "goto": {}, "defer": {}, "go": {}, "select": {},
	"switch": {}, "case": {}, "default": {}, "fallthrough": {}, "new": {}, "make": {},
	"nil": {}, "true": {}, "false": {}, "null": {}, "undefined": {}, "this": {},
	"that": {}, "super": {}, "async": {}, "await": {}, "let": {}, "class": {},
	"def": {}, "self": {}, "void": {}, "int": {}, "string": {}, "bool": {},
	"byte": {}, "rune": {}, "float": {}, "double": {}, "long": {}, "short": {},
	"error": {}, "err": {}, "ctx": {}, "context": {}, "public": {}, "private": {},
	"protected": {}, "static": {}, "final": {}, "extends": {}, "implements": {},
	"export": {}, "from": {}, "module": {}, "require": {}, "exports": {},
	"try": {}, "catch": {}, "finally": {}, "throw": {}, "with": {}, "in": {},
	"is": {}, "not": {}, "and": {}, "or": {}, "pass": {}, "lambda": {}, "yield": {},
	"raise": {}, "assert": {}, "global": {}, "del": {}, "except": {}, "print": {},
	"println": {}, "log": {}, "logs": {}, "req": {}, "res": {}, "response": {},
	"request": {}, "args": {}, "kwargs": {}, "params": {}, "query": {}, "path": {},
	"id": {}, "ids": {}, "name": {}, "value": {}, "values": {}, "result": {},
	"results": {}, "data": {}, "file": {}, "files": {}, "config": {}, "options": {},
	"opts": {}, "init": {}, "main": {}, "get": {}, "set": {}, "put": {}, "post": {},
	"delete": {}, "list": {}, "user": {}, "users": {}, "session": {}, "client": {},
	"server": {}, "handler": {}, "handlers": {}, "service": {}, "services": {},
	"store": {}, "stores": {}, "repo": {}, "repos": {}, "now": {},
	"count": {}, "total": {}, "index": {}, "item": {}, "items": {}, "key": {},
	"keys": {}, "msg": {}, "message": {}, "status": {}, "code": {}, "codes": {},
	"time": {}, "day": {}, "date": {}, "year": {}, "url": {}, "uri": {}, "host": {},
	"port": {}, "body": {}, "input": {}, "output": {}, "action": {}, "actions": {},
	"method": {}, "field": {}, "fields": {}, "row": {}, "rows": {}, "page": {},
	"size": {}, "limit": {}, "offset": {}, "order": {}, "sort": {}, "first": {},
	"last": {}, "next": {}, "prev": {}, "all": {}, "any": {}, "some": {}, "none": {},
	"each": {}, "every": {}, "both": {}, "other": {}, "another": {}, "more": {},
	"less": {}, "most": {}, "least": {}, "many": {}, "few": {}, "one": {}, "two": {},
	"three": {}, "zero": {}, "current": {}, "old": {},
	"length": {}, "sum": {}, "avg": {}, "min": {}, "max": {},
	"found": {}, "exists": {}, "empty": {}, "ok": {}, "done": {}, "start": {},
	"end": {}, "begin": {}, "stop": {}, "run": {}, "runs": {}, "call": {}, "calls": {},
	"create": {}, "creates": {}, "update": {}, "updates": {}, "remove": {},
	"removes": {}, "add": {}, "adds": {}, "check": {}, "checks": {}, "load": {},
	"loads": {}, "save": {}, "saves": {}, "read": {}, "reads": {}, "write": {},
	"writes": {}, "open": {}, "close": {}, "closes": {}, "parse": {}, "parses": {},
	"format": {}, "formats": {}, "number": {}, "bytes": {}, "object": {}, "objects": {},
	"array": {}, "arrays": {}, "tuple": {}, "dict": {}, "dictionary": {},
	"dir": {}, "dirs": {}, "temp": {}, "tmp": {}, "text": {},
	"info": {}, "state": {}, "mode": {}, "kind": {},
	"version": {}, "versions": {}, "label": {}, "labels": {}, "tag": {},
	"tags": {}, "hash": {}, "hashes": {}, "signature": {}, "meta": {}, "metadata": {},
	"address": {}, "email": {}, "phone": {}, "zip": {}, "city": {},
	"country": {}, "locale": {}, "lang": {}, "langs": {}, "source": {}, "target": {},
	"destination": {}, "origin": {}, "role": {}, "roles": {}, "permission": {},
	"permissions": {}, "admin": {}, "owner": {}, "owners": {}, "customer": {},
	"customers": {}, "orders": {}, "product": {}, "products": {},
	"account": {}, "accounts": {}, "org": {}, "orgs": {}, "team": {}, "teams": {},
	"member": {}, "members": {}, "project": {}, "projects": {}, "issue": {},
	"issues": {}, "ticket": {}, "tickets": {}, "task": {}, "tasks": {}, "job": {},
	"jobs": {}, "work": {}, "works": {}, "event": {}, "events": {},
	"content": {}, "header": {}, "headers": {}, "cookie": {}, "cookies": {},
	"token": {}, "tokens": {}, "auth": {}, "authn": {}, "authz": {}, "login": {},
	"logout": {}, "register": {}, "registration": {}, "password": {}, "secret": {},
	"secrets": {}, "conf": {}, "settings": {}, "setting": {},
}

// DiffSymbols extracts candidate code symbols from the added lines of a change
// set: identifiers and import package names a deep specialist may need
// surrounding definitions for. Deterministic and bounded: tokens are
// frequency-ranked, generic words are dropped, and the result is capped.
func DiffSymbols(changes change.ChangeSet) []string {
	counts := make(map[string]int)
	for _, file := range changes.Files {
		for _, hunk := range file.Hunks {
			for _, line := range hunk.Lines {
				if line.Kind != change.LineAdded {
					continue
				}
				for _, token := range symbolsFromLine(line.Content) {
					counts[token]++
				}
			}
		}
	}
	if len(counts) == 0 {
		return nil
	}
	tokens := make([]string, 0, len(counts))
	for token := range counts {
		tokens = append(tokens, token)
	}
	sort.Slice(tokens, func(i, j int) bool {
		if counts[tokens[i]] != counts[tokens[j]] {
			return counts[tokens[i]] > counts[tokens[j]]
		}
		return tokens[i] < tokens[j]
	})
	if len(tokens) > maxDiffSymbols {
		tokens = tokens[:maxDiffSymbols]
	}
	return tokens
}

func symbolsFromLine(content string) []string {
	var symbols []string
	seen := make(map[string]struct{})
	consider := func(token string) {
		if _, exists := seen[token]; exists {
			return
		}
		if len(token) < 3 {
			return
		}
		if strings.Trim(token, "_0123456789") == "" {
			return
		}
		if _, generic := commonWords[token]; generic {
			return
		}
		seen[token] = struct{}{}
		symbols = append(symbols, token)
	}
	// Import package names come from the original line; identifiers are
	// scanned on the literal-stripped remainder so import paths and messages
	// never leak into the symbol set as themselves.
	for _, segment := range importSegments(content) {
		consider(segment)
	}
	for _, token := range identifierPattern.FindAllString(stripStringLiterals(content), -1) {
		consider(token)
	}
	return symbols
}

// stripStringLiterals removes quoted strings so identifiers inside imports
// and messages never leak into the symbol set as themselves.
func stripStringLiterals(content string) string {
	return stringLiteral.ReplaceAllString(content, "")
}

// importSegments returns the package name of each import-looking string
// literal ("path/to/package") found in the content.
func importSegments(content string) []string {
	var segments []string
	for _, match := range stringLiteral.FindAllStringSubmatch(content, -1) {
		if len(match) != 2 {
			continue
		}
		value := match[1]
		if !strings.Contains(value, "/") || strings.ContainsAny(value, " \t") {
			continue
		}
		parts := strings.Split(value, "/")
		last := strings.Trim(parts[len(parts)-1], ".")
		if last != "" {
			segments = append(segments, last)
		}
	}
	return segments
}
