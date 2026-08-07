"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.providers = void 0;
exports.providerByID = providerByID;
exports.providers = [
    {
        id: "openai",
        label: "OpenAI",
        description: "OpenAI Responses API with strict structured output",
        defaultModel: "gpt-4.1-mini",
        models: [
            { label: "gpt-4.1-mini", description: "Fast, lower-cost code review" },
            { label: "gpt-4.1", description: "Higher-capability code review" },
            { label: "o4-mini", description: "Reasoning-focused review" }
        ],
        environmentVariable: "REVIEWER_OPENAI_API_KEY",
        secretKey: "code-review.provider.openai.apiKey"
    },
    {
        id: "deepseek",
        label: "DeepSeek",
        description: "DeepSeek Chat Completions API with JSON output",
        defaultModel: "deepseek-chat",
        models: [
            { label: "deepseek-chat", description: "General code review" },
            { label: "deepseek-reasoner", description: "Reasoning-focused review" }
        ],
        environmentVariable: "REVIEWER_DEEPSEEK_API_KEY",
        secretKey: "code-review.provider.deepseek.apiKey"
    }
];
function providerByID(value) {
    return exports.providers.find(provider => provider.id === value);
}
