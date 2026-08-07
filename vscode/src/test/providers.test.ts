import assert from "node:assert/strict";
import test from "node:test";

import { providerByID, providers } from "../providers";

test("provider registry exposes separate secure configurations", () => {
  assert.deepEqual(providers.map(provider => provider.id), ["openai", "deepseek"]);
  assert.equal(providerByID("deepseek")?.defaultModel, "deepseek-chat");
  assert.notEqual(providerByID("openai")?.secretKey, providerByID("deepseek")?.secretKey);
  assert.notEqual(providerByID("openai")?.environmentVariable, providerByID("deepseek")?.environmentVariable);
  assert.equal(providerByID("unknown"), undefined);
});
