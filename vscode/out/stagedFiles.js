"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.parseNameStatus = parseNameStatus;
function parseNameStatus(output) {
    const tokens = output.split("\0");
    const files = [];
    let index = 0;
    while (index < tokens.length && tokens[index] !== "") {
        const status = tokens[index++];
        if (!/^[ACDMRTUXB][0-9]*$/.test(status)) {
            throw new Error(`Git returned an unsupported staged status ${JSON.stringify(status)}`);
        }
        if (status.startsWith("R") || status.startsWith("C")) {
            const previousPath = tokens[index++];
            const filePath = tokens[index++];
            if (!previousPath || !filePath) {
                throw new Error("Git returned an incomplete staged rename or copy");
            }
            files.push({ path: filePath, previousPath, status });
            continue;
        }
        const filePath = tokens[index++];
        if (!filePath) {
            throw new Error("Git returned an incomplete staged file entry");
        }
        files.push({ path: filePath, status });
    }
    return files;
}
