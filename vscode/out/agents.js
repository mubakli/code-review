"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.defaultReviewAgentIDs = exports.reviewAgents = void 0;
exports.configuredReviewAgentIDs = configuredReviewAgentIDs;
exports.reviewAgentSummary = reviewAgentSummary;
exports.reviewAgents = [
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
exports.defaultReviewAgentIDs = exports.reviewAgents.map(agent => agent.id);
function configuredReviewAgentIDs(values) {
    const supported = new Set(exports.reviewAgents.map(agent => agent.id));
    const selected = values.filter((value, index) => supported.has(value) && values.indexOf(value) === index);
    return selected.length > 0 ? selected : [...exports.defaultReviewAgentIDs];
}
function reviewAgentSummary(ids) {
    return exports.reviewAgents
        .filter(agent => ids.includes(agent.id))
        .map(agent => agent.label)
        .join(" + ");
}
