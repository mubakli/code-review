import * as vscode from "vscode";

interface GitExtension {
  enabled: boolean;
  onDidChangeEnablement: vscode.Event<boolean>;
  getAPI(version: 1): GitAPI;
}

interface GitAPI {
  repositories: GitRepository[];
  onDidOpenRepository: vscode.Event<GitRepository>;
  onDidCloseRepository: vscode.Event<GitRepository>;
}

interface GitRepository {
  rootUri: vscode.Uri;
  state: {
    onDidChange: vscode.Event<void>;
  };
}

export async function watchGitRepositories(
  onStagedStateHint: (root: vscode.Uri) => void
): Promise<vscode.Disposable> {
  const disposables: vscode.Disposable[] = [];
  const repositorySubscriptions = new Map<string, vscode.Disposable>();
  const extension = vscode.extensions.getExtension<GitExtension>("vscode.git");
  if (extension === undefined) {
    return new vscode.Disposable(() => {});
  }
  const git = await extension.activate();
  const api = git.getAPI(1);

  const attach = (repository: GitRepository): void => {
    const key = repository.rootUri.toString();
    if (repositorySubscriptions.has(key)) {
      return;
    }
    const subscription = repository.state.onDidChange(() => onStagedStateHint(repository.rootUri));
    repositorySubscriptions.set(key, subscription);
    onStagedStateHint(repository.rootUri);
  };
  const detach = (repository: GitRepository): void => {
    const key = repository.rootUri.toString();
    repositorySubscriptions.get(key)?.dispose();
    repositorySubscriptions.delete(key);
  };

  for (const repository of api.repositories) {
    attach(repository);
  }
  disposables.push(api.onDidOpenRepository(attach), api.onDidCloseRepository(detach));
  return new vscode.Disposable(() => {
    for (const disposable of disposables) {
      disposable.dispose();
    }
    for (const subscription of repositorySubscriptions.values()) {
      subscription.dispose();
    }
    repositorySubscriptions.clear();
  });
}
