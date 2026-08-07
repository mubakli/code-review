import * as path from "node:path";

import Mocha from "mocha";

export async function run(): Promise<void> {
  const mocha = new Mocha({
    color: true,
    timeout: 30_000,
    ui: "tdd"
  });
  mocha.addFile(path.resolve(__dirname, "extension.test.js"));

  await new Promise<void>((resolve, reject) => {
    mocha.run(failures => {
      if (failures > 0) {
        reject(new Error(`${failures} VS Code integration test(s) failed`));
        return;
      }
      resolve();
    });
  });
}
