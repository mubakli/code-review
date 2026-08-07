"use strict";
var __importDefault = (this && this.__importDefault) || function (mod) {
    return (mod && mod.__esModule) ? mod : { "default": mod };
};
Object.defineProperty(exports, "__esModule", { value: true });
const strict_1 = __importDefault(require("node:assert/strict"));
const node_test_1 = __importDefault(require("node:test"));
const providers_1 = require("../providers");
(0, node_test_1.default)("provider registry exposes separate secure configurations", () => {
    strict_1.default.deepEqual(providers_1.providers.map(provider => provider.id), ["openai", "deepseek"]);
    strict_1.default.equal((0, providers_1.providerByID)("deepseek")?.defaultModel, "deepseek-chat");
    strict_1.default.notEqual((0, providers_1.providerByID)("openai")?.secretKey, (0, providers_1.providerByID)("deepseek")?.secretKey);
    strict_1.default.notEqual((0, providers_1.providerByID)("openai")?.environmentVariable, (0, providers_1.providerByID)("deepseek")?.environmentVariable);
    strict_1.default.equal((0, providers_1.providerByID)("unknown"), undefined);
});
