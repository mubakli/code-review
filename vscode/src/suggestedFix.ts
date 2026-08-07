import { ProposedFix } from "./protocol";

export function applyProposedFix(content: string, fix: ProposedFix): string {
  const lines = lineRanges(content);
  const start = lines[fix.startLine - 1];
  const end = lines[fix.endLine - 1];
  if (start === undefined || end === undefined) {
    throw new Error("suggested fix range is outside the staged file");
  }
  const eol = content.includes("\r\n") ? "\r\n" : "\n";
  let replacement = normalizeEOL(fix.replacement, eol);
  if (replacement !== "" && end.lineEnd > end.contentEnd && !replacement.endsWith(eol)) {
    replacement += eol;
  }
  return content.slice(0, start.start) + replacement + content.slice(end.lineEnd);
}

interface LineRange {
  start: number;
  contentEnd: number;
  lineEnd: number;
}

function lineRanges(content: string): LineRange[] {
  const result: LineRange[] = [];
  let start = 0;
  for (let index = 0; index < content.length; index++) {
    if (content[index] !== "\n") {
      continue;
    }
    result.push({
      start,
      contentEnd: index > start && content[index - 1] === "\r" ? index - 1 : index,
      lineEnd: index + 1
    });
    start = index + 1;
  }
  result.push({ start, contentEnd: content.length, lineEnd: content.length });
  return result;
}

function normalizeEOL(value: string, eol: "\n" | "\r\n"): string {
  return value.replace(/\r\n|\r|\n/g, eol);
}
