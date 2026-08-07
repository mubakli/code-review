export const reviewSchemaVersion = 3;

export type FindingSeverity = "critical" | "high" | "medium" | "low" | "info";
export type FindingCategory = "security" | "correctness" | "performance" | "database" | "maintainability" | "quality";
export type FindingSource = "local-rule" | "static-analysis" | "sql-analyzer" | "ai";

export interface ProposedFix {
  description: string;
  startLine: number;
  endLine: number;
  replacement: string;
}

export interface ReviewFinding {
  ruleId: string;
  findingId: string;
  file: string;
  startLine: number;
  endLine: number;
  severity: FindingSeverity;
  category: FindingCategory;
  title: string;
  message: string;
  suggestion?: string;
  proposedFix?: ProposedFix;
  confidence: number;
  source: FindingSource;
  agentId?: string;
}

export interface ReviewSummary {
  filesChanged: number;
  filesReviewed: number;
  filesSkipped: number;
  hunksReviewed: number;
  addedLines: number;
  deletedLines: number;
  findingCount: number;
}

export interface AIReviewSummary {
  provider: string;
  model: string;
  reviewedFiles: string[];
  agents: string[];
  batchCount: number;
  successfulBatches: number;
  failedBatches: number;
}

export interface ReviewResult {
  schemaVersion: number;
  reviewId: string;
  summary: ReviewSummary;
  files: ReviewFile[];
  findings: ReviewFinding[];
  ai?: AIReviewSummary;
}

export interface ReviewFile {
  path: string;
  previousPath?: string;
  status: "modified" | "added" | "deleted" | "renamed" | "copied";
  binary?: boolean;
}

export interface SnapshotResult {
  schemaVersion: number;
  reviewId: string;
  filesChanged: number;
}

const severities = new Set<FindingSeverity>(["critical", "high", "medium", "low", "info"]);
const categories = new Set(["security", "correctness", "performance", "database", "maintainability", "quality"]);
const sources = new Set(["local-rule", "static-analysis", "sql-analyzer", "ai"]);

export function parseReviewResult(output: string): ReviewResult {
  let value: unknown;
  try {
    value = JSON.parse(output);
  } catch (error) {
    throw new Error(`reviewer returned invalid JSON: ${errorMessage(error)}`);
  }
  const root = record(value, "review result");
  const schemaVersion = integer(root.schemaVersion, "schemaVersion");
  if (schemaVersion !== reviewSchemaVersion) {
    throw new Error(`unsupported review schema version ${schemaVersion}`);
  }

  const summaryValue = record(root.summary, "summary");
  const summary: ReviewSummary = {
    filesChanged: nonNegativeInteger(summaryValue.filesChanged, "summary.filesChanged"),
    filesReviewed: nonNegativeInteger(summaryValue.filesReviewed, "summary.filesReviewed"),
    filesSkipped: nonNegativeInteger(summaryValue.filesSkipped, "summary.filesSkipped"),
    hunksReviewed: nonNegativeInteger(summaryValue.hunksReviewed, "summary.hunksReviewed"),
    addedLines: nonNegativeInteger(summaryValue.addedLines, "summary.addedLines"),
    deletedLines: nonNegativeInteger(summaryValue.deletedLines, "summary.deletedLines"),
    findingCount: nonNegativeInteger(summaryValue.findingCount, "summary.findingCount")
  };

  if (!Array.isArray(root.findings)) {
    throw new Error("findings must be an array");
  }
  const findings = root.findings.map(parseFinding);
  if (new Set(findings.map(finding => finding.findingId)).size !== findings.length) {
    throw new Error("findingId values must be unique");
  }
  if (summary.findingCount !== findings.length) {
    throw new Error("summary.findingCount does not match findings length");
  }

  if (!Array.isArray(root.files)) {
    throw new Error("files must be an array");
  }
  return {
    schemaVersion,
    reviewId: reviewID(root.reviewId),
    summary,
    files: root.files.map(parseReviewFile),
    findings,
    ai: root.ai === undefined ? undefined : parseAI(root.ai)
  };
}

export function parseSnapshotResult(output: string): SnapshotResult {
  let value: unknown;
  try {
    value = JSON.parse(output);
  } catch (error) {
    throw new Error(`reviewer returned invalid snapshot JSON: ${errorMessage(error)}`);
  }
  const snapshot = record(value, "snapshot result");
  const schemaVersion = integer(snapshot.schemaVersion, "schemaVersion");
  if (schemaVersion !== reviewSchemaVersion) {
    throw new Error(`unsupported review schema version ${schemaVersion}`);
  }
  return {
    schemaVersion,
    reviewId: reviewID(snapshot.reviewId),
    filesChanged: nonNegativeInteger(snapshot.filesChanged, "filesChanged")
  };
}

function parseReviewFile(value: unknown, index: number): ReviewFile {
  const prefix = `files[${index}]`;
  const file = record(value, prefix);
  const status = text(file.status, `${prefix}.status`) as ReviewFile["status"];
  if (!new Set(["modified", "added", "deleted", "renamed", "copied"]).has(status)) {
    throw new Error(`${prefix}.status is unsupported`);
  }
  return {
    path: singleLineText(file.path, `${prefix}.path`),
    previousPath: file.previousPath === undefined ? undefined : singleLineText(file.previousPath, `${prefix}.previousPath`),
    status,
    binary: file.binary === undefined ? undefined : boolean(file.binary, `${prefix}.binary`)
  };
}

function parseFinding(value: unknown, index: number): ReviewFinding {
  const prefix = `findings[${index}]`;
  const finding = record(value, prefix);
  const severity = text(finding.severity, `${prefix}.severity`) as FindingSeverity;
  if (!severities.has(severity)) {
    throw new Error(`${prefix}.severity is unsupported`);
  }
  const startLine = positiveInteger(finding.startLine, `${prefix}.startLine`);
  const endLine = positiveInteger(finding.endLine, `${prefix}.endLine`);
  if (endLine < startLine) {
    throw new Error(`${prefix}.endLine precedes startLine`);
  }
  const confidence = finiteNumber(finding.confidence, `${prefix}.confidence`);
  if (confidence < 0 || confidence > 1) {
    throw new Error(`${prefix}.confidence must be between 0 and 1`);
  }
  const category = text(finding.category, `${prefix}.category`) as FindingCategory;
  if (!categories.has(category)) {
    throw new Error(`${prefix}.category is unsupported`);
  }
  const source = text(finding.source, `${prefix}.source`) as FindingSource;
  if (!sources.has(source)) {
    throw new Error(`${prefix}.source is unsupported`);
  }
  return {
    ruleId: ruleID(finding.ruleId, `${prefix}.ruleId`),
    findingId: findingID(finding.findingId, `${prefix}.findingId`),
    file: relativePath(finding.file, `${prefix}.file`),
    startLine,
    endLine,
    severity,
    category,
    title: text(finding.title, `${prefix}.title`),
    message: text(finding.message, `${prefix}.message`),
    suggestion: optionalText(finding.suggestion, `${prefix}.suggestion`),
    proposedFix: finding.proposedFix === undefined ? undefined : parseProposedFix(finding.proposedFix, prefix, startLine, endLine),
    confidence,
    source,
    agentId: finding.agentId === undefined ? undefined : singleLineText(finding.agentId, `${prefix}.agentId`)
  };
}

function parseProposedFix(value: unknown, prefix: string, findingStart: number, findingEnd: number): ProposedFix {
  const fix = record(value, `${prefix}.proposedFix`);
  const startLine = positiveInteger(fix.startLine, `${prefix}.proposedFix.startLine`);
  const endLine = positiveInteger(fix.endLine, `${prefix}.proposedFix.endLine`);
  if (startLine !== findingStart || endLine !== findingEnd) {
    throw new Error(`${prefix}.proposedFix range must match the finding range`);
  }
  const description = text(fix.description, `${prefix}.proposedFix.description`);
  const replacement = string(fix.replacement, `${prefix}.proposedFix.replacement`);
  if (byteLength(description) > 1000) {
    throw new Error(`${prefix}.proposedFix.description is too large`);
  }
  if (byteLength(replacement) > 64 * 1024) {
    throw new Error(`${prefix}.proposedFix.replacement is too large`);
  }
  if (containsUnsafeFixMarker(description) || containsUnsafeFixMarker(replacement)) {
    throw new Error(`${prefix}.proposedFix contains a redaction or truncation marker`);
  }
  return { description, startLine, endLine, replacement };
}

function parseAI(value: unknown): AIReviewSummary {
  const ai = record(value, "ai");
  return {
    provider: text(ai.provider, "ai.provider"),
    model: text(ai.model, "ai.model"),
    reviewedFiles: ai.reviewedFiles === undefined ? [] : textArray(ai.reviewedFiles, "ai.reviewedFiles"),
    agents: ai.agents === undefined ? [] : textArray(ai.agents, "ai.agents"),
    batchCount: nonNegativeInteger(ai.batchCount, "ai.batchCount"),
    successfulBatches: nonNegativeInteger(ai.successfulBatches, "ai.successfulBatches"),
    failedBatches: nonNegativeInteger(ai.failedBatches, "ai.failedBatches")
  };
}

function textArray(value: unknown, name: string): string[] {
  if (!Array.isArray(value)) {
    throw new Error(`${name} must be an array`);
  }
  return value.map((entry, index) => singleLineText(entry, `${name}[${index}]`));
}

function record(value: unknown, name: string): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error(`${name} must be an object`);
  }
  return value as Record<string, unknown>;
}

function text(value: unknown, name: string): string {
  if (typeof value !== "string" || value.trim() === "") {
    throw new Error(`${name} must be a non-empty string`);
  }
  return value;
}

function string(value: unknown, name: string): string {
  if (typeof value !== "string" || value.includes("\0")) {
    throw new Error(`${name} must be NUL-free text`);
  }
  return value;
}

function optionalText(value: unknown, name: string): string | undefined {
  if (value === undefined) {
    return undefined;
  }
  return text(value, name);
}

function singleLineText(value: unknown, name: string): string {
  const result = text(value, name);
  if (/\p{C}/u.test(result)) {
    throw new Error(`${name} contains control characters`);
  }
  return result;
}

function finiteNumber(value: unknown, name: string): number {
  if (typeof value !== "number" || !Number.isFinite(value)) {
    throw new Error(`${name} must be a finite number`);
  }
  return value;
}

function boolean(value: unknown, name: string): boolean {
  if (typeof value !== "boolean") {
    throw new Error(`${name} must be a boolean`);
  }
  return value;
}

function reviewID(value: unknown): string {
  const result = singleLineText(value, "reviewId");
  if (!/^sha256:[0-9a-f]{64}$/.test(result)) {
    throw new Error("reviewId must be a SHA-256 identifier");
  }
  return result;
}

function findingID(value: unknown, name: string): string {
  const result = singleLineText(value, name);
  if (!/^sha256:[0-9a-f]{64}$/.test(result)) {
    throw new Error(`${name} must be a SHA-256 identifier`);
  }
  return result;
}

function ruleID(value: unknown, name: string): string {
  const result = singleLineText(value, name);
  if (new TextEncoder().encode(result).length > 128 || !/^[a-z][a-z0-9-]*(\/[a-z][a-z0-9-]*)+$/.test(result)) {
    throw new Error(`${name} must be a safe lowercase namespace`);
  }
  return result;
}

function relativePath(value: unknown, name: string): string {
  const result = singleLineText(value, name);
  if (result.startsWith("/") || /^[a-zA-Z]:[\\/]/.test(result) || result.split(/[\\/]/).includes("..")) {
    throw new Error(`${name} must be repository-relative`);
  }
  return result;
}

function byteLength(value: string): number {
  return new TextEncoder().encode(value).length;
}

function containsUnsafeFixMarker(value: string): boolean {
  const lower = value.toLowerCase();
  return ["[redacted", "<redacted", "[truncated", "<truncated", "[reviewer: partial hunk", "[reviewer: long diff line truncated"]
    .some(marker => lower.includes(marker));
}

function integer(value: unknown, name: string): number {
  const result = finiteNumber(value, name);
  if (!Number.isInteger(result)) {
    throw new Error(`${name} must be an integer`);
  }
  return result;
}

function positiveInteger(value: unknown, name: string): number {
  const result = integer(value, name);
  if (result < 1) {
    throw new Error(`${name} must be positive`);
  }
  return result;
}

function nonNegativeInteger(value: unknown, name: string): number {
  const result = integer(value, name);
  if (result < 0) {
    throw new Error(`${name} must not be negative`);
  }
  return result;
}

function errorMessage(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
