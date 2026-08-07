export type ProviderID = "openai" | "deepseek";

export interface ProviderDefinition {
  id: ProviderID;
  label: string;
  description: string;
  defaultModel: string;
  models: Array<{ label: string; description: string }>;
  environmentVariable: string;
  secretKey: string;
}

export const providers: ProviderDefinition[] = [
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

export function providerByID(value: string): ProviderDefinition | undefined {
  return providers.find(provider => provider.id === value);
}
