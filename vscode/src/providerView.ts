import * as vscode from "vscode";

import { ReviewAgentID, reviewAgentSummary } from "./agents";
import { ProviderDefinition } from "./providers";

export const providerViewID = "code-review.providerView";
export const configureProviderCommand = "code-review.configureAIProvider";
export const selectModelCommand = "code-review.selectAIModel";
export const manageAPIKeyCommand = "code-review.manageAIKey";
export const selectAgentsCommand = "code-review.selectAIAgents";

export interface ProviderViewState {
  agents: ReviewAgentID[];
  autoReview: boolean;
  enabled: boolean;
  keyStored: boolean;
  model: string;
  provider?: ProviderDefinition;
}

export class ProviderTreeProvider implements vscode.TreeDataProvider<vscode.TreeItem> {
  private readonly changedEmitter = new vscode.EventEmitter<void>();
  private state: ProviderViewState = {
    agents: [],
    autoReview: false,
    enabled: false,
    keyStored: false,
    model: ""
  };

  readonly onDidChangeTreeData = this.changedEmitter.event;

  update(state: ProviderViewState): void {
    this.state = state;
    this.changedEmitter.fire();
  }

  getTreeItem(element: vscode.TreeItem): vscode.TreeItem {
    return element;
  }

  getChildren(): vscode.TreeItem[] {
    if (!this.state.enabled || this.state.provider === undefined) {
      return [item(
        "AI review is off",
        "Choose OpenAI or DeepSeek",
        "circle-slash",
        configureProviderCommand,
        "Choose AI Provider"
      )];
    }
    const provider = this.state.provider;
    return [
      item(provider.label, "Selected provider", "sparkle", configureProviderCommand, "Change AI Provider"),
      item(this.state.model || provider.defaultModel, "Model", "symbol-method", selectModelCommand, "Change AI Model"),
      item(
        reviewAgentSummary(this.state.agents),
        "Review agents",
        "organization",
        selectAgentsCommand,
        "Select AI Review Agents"
      ),
      item(
        this.state.keyStored ? "API key stored" : "API key required",
        "SecretStorage",
        this.state.keyStored ? "key" : "warning",
        manageAPIKeyCommand,
        "Manage API Key"
      ),
      item(
        this.state.autoReview ? "Automatic review on" : "Automatic review off",
        "After staged changes",
        this.state.autoReview ? "sync" : "debug-pause",
        configureProviderCommand,
        "Configure AI Review"
      )
    ];
  }
}

function item(
  label: string,
  description: string,
  icon: string,
  command: string,
  title: string
): vscode.TreeItem {
  const value = new vscode.TreeItem(label, vscode.TreeItemCollapsibleState.None);
  value.description = description;
  value.iconPath = new vscode.ThemeIcon(icon);
  value.command = { command, title };
  return value;
}
