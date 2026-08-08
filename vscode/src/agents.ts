export type ReviewAgentID = "correctness" | "security";

export interface ReviewAgentDefinition {
  id: ReviewAgentID;
  label: string;
  description: string;
}

export const reviewAgents: ReviewAgentDefinition[] = [
  {
    id: "correctness",
    label: "Correctness",
    description: "Edge cases, errors, resources, concurrency, and incorrect assumptions"
  },
  {
    id: "security",
    label: "Security",
    description: "Lightweight triage on every change; deep review with staged context only when attack surface is detected"
  }
];

export const defaultReviewAgentIDs: ReviewAgentID[] = reviewAgents.map(agent => agent.id);

export function configuredReviewAgentIDs(values: readonly string[]): ReviewAgentID[] {
  const supported = new Set(reviewAgents.map(agent => agent.id));
  const selected = values.filter((value, index): value is ReviewAgentID =>
    supported.has(value as ReviewAgentID) && values.indexOf(value) === index
  );
  return selected.length > 0 ? selected : [...defaultReviewAgentIDs];
}

export function reviewAgentSummary(ids: readonly ReviewAgentID[]): string {
  return reviewAgents
    .filter(agent => ids.includes(agent.id))
    .map(agent => agent.label)
    .join(" + ");
}
