"use strict";
Object.defineProperty(exports, "__esModule", { value: true });
exports.buildReviewerEnvironment = buildReviewerEnvironment;
const allowedEnvironmentKeys = new Set([
    "COMSPEC",
    "HOMEDRIVE",
    "HOMEPATH",
    "HOME",
    "HTTP_PROXY",
    "HTTPS_PROXY",
    "LANG",
    "LC_ALL",
    "NO_PROXY",
    "PATH",
    "PATHEXT",
    "SSL_CERT_DIR",
    "SSL_CERT_FILE",
    "SYSTEMROOT",
    "TEMP",
    "TMP",
    "TMPDIR",
    "USERPROFILE",
    "XDG_CONFIG_HOME"
]);
function buildReviewerEnvironment(source) {
    const environment = {};
    for (const [key, value] of Object.entries(source)) {
        if (allowedEnvironmentKeys.has(key.toUpperCase()) && value !== undefined) {
            environment[key] = value;
        }
    }
    return environment;
}
