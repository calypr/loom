#!/usr/bin/env python3
"""Comment detection backends for scripts/check_no_code_comments.py.

Detection-only language lexers: each backend returns every comment token it
finds in a file, with the classification decision applied separately by the
checker. Detection reuses the machine-law-validated lexers from the lane
comment-removal lever; string, template, heredoc, block-scalar, raw-string and
nesting rules are preserved verbatim from those proven implementations.
"""

import html.parser
import io
import json
import os
import re
import subprocess
import sys
import tokenize
import token as token_mod

TS_FAMILY = {".js", ".mjs", ".cjs", ".mts", ".ts", ".tsx", ".jsx"}

SPECIAL_JSON = {".gitnexusrc"}
SPECIAL_MAKE = {"Makefile", "makefile"}
SPECIAL_IGNORE = {".gitignore", ".dockerignore", ".codexignore", ".gitnexusignore"}
SHELL_KEEP = re.compile(r"^\s*(shellcheck\b|shellcase|shellcheck-disable|vim:|@shlint)")
PY_DIRECTIVE = re.compile(
    r"^\s*#\s*(type:\s|noqa\b|mypy:\s|pylint:\s|fmt:\s|ruff:\s|isort:\s|pragma:|coding[:=])")
GO_DIRECTIVE = re.compile(
    r"^//\s*(go:[a-z][a-z0-9]*|line\s+\d|extern\s|export\s|#cgo\b)")
JS_DIRECTIVE = re.compile(
    r"^(?://|/\*)\s*(@?eslint\b|eslint-\w[\w-]*|@ts-(ignore|expect-error|nocheck|check|strict)\b"
    r"|@vitest-environment\s+\S+|@__PURE__|@vite-ignore|istanbul\b)")
DOCKERFILE_DIRECTIVE = re.compile(r"^#\s*(syntax|escape|check)\s*=")
GOMOD_INDIRECT = re.compile(r"^//\s*indirect\s*$")


def spans_to_comments(src, spans):
    comments = []
    for (a, b) in spans:
        comments.append({
            "line": src.count("\n", 0, a) + 1,
            "kind": "block" if "\n" in src[a:b] or src[a:a + 2] in ("/*", "(*") else "line",
            "text": src[a:b],
        })
    return comments


def go_comment_spans(src):
    spans = []
    i = 0
    n = len(src)
    if src.startswith("#!"):
        j = src.find("\n")
        spans.append((0, j if j != -1 else n))
        i = spans[-1][1]
    while i < n:
        c = src[i]
        if c == "/" and i + 1 < n and src[i + 1] == "/":
            j = src.find("\n", i)
            spans.append((i, j if j != -1 else n))
            i = spans[-1][1]
            continue
        if c == "/" and i + 1 < n and src[i + 1] == "*":
            j = src.find("*/", i + 2)
            if j == -1:
                raise RuntimeError("unterminated /* block comment")
            spans.append((i, j + 2))
            i = j + 2
            continue
        if c == '"':
            i += 1
            while i < n:
                if src[i] == "\\":
                    i += 2
                    continue
                if src[i] == '"':
                    break
                i += 1
            i += 1
            continue
        if c == "`":
            j = src.find("`", i + 1)
            if j == -1:
                raise RuntimeError("unterminated raw string")
            i = j + 1
            continue
        if c == "'":
            i += 1
            while i < n:
                if src[i] == "\\":
                    i += 2
                    continue
                if src[i] == "'":
                    break
                i += 1
            i += 1
            continue
        i += 1
    return spans


def sh_comment_spans(src):
    spans = []
    i = 0
    n = len(src)
    prev = "\n"
    heredoc_delim = None
    heredoc_dash = False
    in_heredoc = False

    def read_line(pos):
        j = src.find("\n", pos)
        return (j + 1 if j != -1 else n), (src[pos:j] if j != -1 else src[pos:])

    while i < n:
        if in_heredoc:
            line_end, line = read_line(i)
            test = line.rstrip("\r")
            if heredoc_dash:
                test = test.lstrip("\t")
            if test == heredoc_delim:
                in_heredoc = False
                heredoc_delim = None
            i = line_end
            continue
        ch = src[i]
        if ch == "\n":
            i += 1
            prev = "\n"
            if heredoc_delim is not None:
                in_heredoc = True
            continue
        if ch == "'":
            j = src.find("'", i + 1)
            if j == -1:
                raise RuntimeError("unterminated single quote")
            i = j + 1
            prev = "'"
            continue
        if ch == '"':
            j = i + 1
            while j < n:
                if src[j] == "\\":
                    j += 2
                    continue
                if src[j] == '"':
                    break
                if src[j] == "`":
                    k = j + 1
                    while k < n and src[k] != "`":
                        k += 2 if src[k] == "\\" else 1
                    j = k
                j += 1
            i = j + 1
            prev = '"'
            continue
        if ch == "`":
            j = i + 1
            while j < n and src[j] != "`":
                j += 2 if src[j] == "\\" else 1
            i = j + 1
            prev = "`"
            continue
        if ch == "$" and i + 1 < n and src[i + 1] == "'":
            j = i + 2
            while j < n:
                if src[j] == "\\":
                    j += 2
                    continue
                if src[j] == "'":
                    break
                j += 1
            i = j + 1
            prev = "'"
            continue
        if ch == "#":
            if prev in set(" \t\n;|&("):
                j = src.find("\n", i)
                if j == -1:
                    j = n
                spans.append((i, j))
                i = j
                prev = "\n"
                continue
        if ch == "<" and i + 1 < n and src[i + 1] == "<":
            m = re.match(r"<<(-?)[ \t]*(['\"]?)([A-Za-z_][A-Za-z0-9_]*)\2", src[i:])
            if m:
                heredoc_dash = m.group(1) == "-"
                heredoc_delim = m.group(3)
                i += m.end()
                prev = heredoc_delim[-1]
                continue
        if ch == "\\" and i + 1 < n and src[i + 1] == "\n":
            i += 2
            continue
        prev = ch
        i += 1
    return spans


def yaml_comment_spans(src):
    spans = []
    lines = src.splitlines(keepends=True)
    block_indent = None
    block_key_re = re.compile(r"^(\s*)(?:[^#]*?:\s*[|>][+-]?\d*)\s*(#.*)?$")
    in_dq = False
    for ln_i, line in enumerate(lines):
        stripped = line.rstrip("\n")
        off = sum(len(l) for l in lines[:ln_i])
        if block_indent is not None:
            if stripped.strip() == "" or (len(stripped) - len(stripped.lstrip())) >= block_indent:
                continue
            block_indent = None
        if in_dq:
            j = 0
            closed = False
            while j < len(stripped):
                if stripped[j] == "\\":
                    j += 2
                    continue
                if stripped[j] == '"':
                    closed = True
                    j += 1
                    break
                j += 1
            if not closed:
                continue
            in_dq = False
            stripped = stripped[j:]
            off += j
        m = block_key_re.match(stripped)
        i = 0
        in_s = in_d2 = False
        found = -1
        while i < len(stripped):
            c = stripped[i]
            if in_s:
                if c == "'":
                    if i + 1 < len(stripped) and stripped[i + 1] == "'":
                        i += 1
                    else:
                        in_s = False
            elif in_d2:
                if c == "\\":
                    i += 1
                elif c == '"':
                    in_d2 = False
            else:
                if c == "'":
                    in_s = True
                elif c == '"':
                    in_d2 = True
                elif c == "#" and (i == 0 or stripped[i - 1] in " \t"):
                    found = i
                    break
            i += 1
        if found >= 0:
            spans.append((off + found, off + len(stripped)))
        if in_d2:
            in_dq = True
        if m and not in_s:
            key_indent = len(m.group(1))
            block_indent = key_indent + 1
    return spans


def toml_comment_spans(src):
    spans = []
    src_lines = src.splitlines(keepends=True)
    in_ml_d = in_ml_s = False
    for ln_i, line in enumerate(src_lines):
        stripped = line.rstrip("\n")
        off = sum(len(l) for l in src_lines[:ln_i])
        if in_ml_d:
            if '"""' in stripped:
                in_ml_d = False
            continue
        if in_ml_s:
            if "'''" in stripped:
                in_ml_s = False
            continue
        i = 0
        in_s = in_d = False
        while i < len(stripped):
            c = stripped[i]
            if in_d:
                if c == "\\":
                    i += 1
                elif c == '"':
                    if stripped[i:i + 3] == '"""':
                        in_ml_d = True
                        break
                    in_d = False
            elif in_s:
                if c == "'":
                    if stripped[i:i + 3] == "'''":
                        in_ml_s = True
                        break
                    in_s = False
            else:
                if c == '"':
                    if stripped[i:i + 3] == chr(34) * 3:
                        in_ml_d = True
                        break
                    in_d = True
                elif c == "'":
                    if stripped[i:i + 3] == chr(39) * 3:
                        in_ml_s = True
                        break
                    in_s = True
                elif c == "#" and (i == 0 or stripped[i - 1] in " \t"):
                    spans.append((off + i, off + len(stripped)))
                    break
            i += 1
    return spans


def tla_comment_spans(src):
    spans = []
    i = 0
    n = len(src)
    while i < n:
        c = src[i]
        if c == '"':
            i += 1
            while i < n:
                if src[i] == "\\":
                    i += 2
                    continue
                if src[i] == '"':
                    if i + 1 < n and src[i + 1] == '"':
                        i += 2
                        continue
                    break
                i += 1
            i += 1
            continue
        if src.startswith("\\*", i):
            j = src.find("\n", i)
            spans.append((i, j if j != -1 else n))
            i = spans[-1][1]
            continue
        if src.startswith("(*", i):
            depth = 1
            j = i + 2
            while j < n and depth:
                if src[j] == '"':
                    j += 1
                    while j < n:
                        if src[j] == "\\":
                            j += 2
                            continue
                        if src[j] == '"':
                            if j + 1 < n and src[j + 1] == '"':
                                j += 2
                                continue
                            break
                        j += 1
                    j += 1
                    continue
                if src.startswith("(*", j):
                    depth += 1
                    j += 2
                elif src.startswith("*)", j):
                    depth -= 1
                    j += 2
                else:
                    j += 1
            if depth != 0:
                raise RuntimeError("unbalanced (* block comment")
            spans.append((i, j))
            i = j
            continue
        if c == "%" and (i == 0 or src[i - 1] == "\n"):
            j = src.find("\n", i)
            spans.append((i, j if j != -1 else n))
            i = spans[-1][1]
            continue
        i += 1
    return spans


def graphql_comment_spans(src):
    spans = []
    i = 0
    n = len(src)
    while i < n:
        if src.startswith('"""', i):
            j = src.find('"""', i + 3)
            i = (j + 3) if j != -1 else n
            continue
        if src[i] == '"':
            i += 1
            while i < n:
                if src[i] == "\\":
                    i += 2
                    continue
                if src[i] == '"':
                    break
                i += 1
            i += 1
            continue
        if src[i] == "#":
            j = src.find("\n", i)
            spans.append((i, j if j != -1 else n))
            i = spans[-1][1]
            continue
        i += 1
    return spans


def sql_comment_spans(src):
    spans = []
    i = 0
    n = len(src)
    while i < n:
        c = src[i]
        if c == "'" :
            i += 1
            while i < n:
                if src[i] == "'":
                    if i + 1 < n and src[i + 1] == "'":
                        i += 2
                        continue
                    break
                i += 1
            i += 1
            continue
        if c == '"':
            i += 1
            while i < n and src[i] != '"':
                i += 1
            i += 1
            continue
        m = re.match(r"\$[A-Za-z_0-9]*\$", src[i:])
        if m and (i == 0 or src[i - 1] in " \t\n("):
            tag = m.group(0)
            j = src.find(tag, i + len(tag))
            if j != -1:
                i = j + len(tag)
                continue
        if src.startswith("--", i):
            j = src.find("\n", i)
            spans.append((i, j if j != -1 else n))
            i = spans[-1][1]
            continue
        if src.startswith("/*", i):
            depth = 1
            j = i + 2
            while j < n and depth:
                if src.startswith("/*", j):
                    depth += 1
                    j += 2
                elif src.startswith("*/", j):
                    depth -= 1
                    j += 2
                else:
                    j += 1
            if depth != 0:
                raise RuntimeError("unterminated nested block comment")
            spans.append((i, j))
            i = j
            continue
        i += 1
    return spans


def css_comment_spans(src):
    spans = []
    i = 0
    n = len(src)
    while i < n:
        c = src[i]
        if c == '"' or c == "'":
            q = c
            i += 1
            while i < n and src[i] != q:
                i += 2 if src[i] == "\\" else 1
            i += 1
            continue
        if src.startswith("/*", i):
            j = src.find("*/", i + 2)
            if j == -1:
                raise RuntimeError("unterminated /* block comment")
            spans.append((i, j + 2))
            i = j + 2
            continue
        i += 1
    return spans


def tmpl_comment_spans(src):
    spans = []
    i = 0
    n = len(src)
    while i < n:
        j = src.find("{{", i)
        if j == -1:
            break
        k = src.find("}}", j + 2)
        inner_start = j + 2
        inner_end = k if k != -1 else n
        body = src[inner_start:inner_end]
        body = body[1:] if body.startswith("-") else body
        body = body[:-1] if body.endswith("-") else body
        body = body.strip()
        if body.startswith("/*") and body.endswith("*/"):
            spans.append((j, inner_end if k != -1 else n))
            i = inner_end
            continue
        i = (k + 2) if k != -1 else n
    return spans


def py_comment_spans(src):
    spans = []
    try:
        toks = list(tokenize.generate_tokens(io.StringIO(src).readline))
    except (tokenize.TokenError, IndentationError, SyntaxError) as exc:
        raise RuntimeError(f"python tokenize failed: {exc}") from exc
    for t in toks:
        if t.type == token_mod.COMMENT:
            (srow, scol), (erow, ecol) = t.start, t.end
            a = sum(len(l) for l in src.splitlines(keepends=True)[:srow - 1]) + scol
            b = sum(len(l) for l in src.splitlines(keepends=True)[:erow - 1]) + ecol
            spans.append((a, b))
    return spans


def prefix_line_spans(src, prefixes):
    spans = []
    off = 0
    for line in src.splitlines(keepends=True):
        body = line.lstrip()
        indent = len(line) - len(body)
        if any(body.startswith(p) for p in prefixes):
            spans.append((off + indent, off + len(line.rstrip("\n"))))
        off += len(line)
    return spans


def detect_json(path, src):
    json.loads(src)
    return []


def detect_ndjson(path, src):
    for line in src.splitlines():
        if line.strip():
            json.loads(line)
    return []


def detect_html(path, src):
    found = []

    class P(html.parser.HTMLParser):
        def handle_comment(self, data):
            found.append(data)
    P().feed(src)
    return [{"line": src.count("\n", 0, src.find("<!--")) + 1, "kind": "block",
             "text": "<!--" + d[:60]} for d in found]


def detect_sum(path, src):
    pat = re.compile(r"^\S+ \S+ (h1:|go1:)?\S+ ?(\S+)?\n?$")
    for i, line in enumerate(src.splitlines(), 1):
        if not pat.match(line):
            raise RuntimeError(f"go.sum line {i} unexpected format")
    return []


TS_SCANNER_JS = """
import {createRequire} from 'module';
const require = createRequire(import.meta.url);
let ts = null;
for (const p of JSON.parse(process.argv[2])) {
  try { ts = require(p); break; } catch { }
}
if (!ts) { console.log(JSON.stringify({error: 'typescript module not found'})); process.exit(0); }
const paths = JSON.parse(process.argv[3]);
const kinds = {'.ts': ts.ScriptKind.TS, '.tsx': ts.ScriptKind.TSX, '.js': ts.ScriptKind.JS,
  '.mjs': ts.ScriptKind.JS, '.cjs': ts.ScriptKind.JS, '.mts': ts.ScriptKind.JS, '.jsx': ts.ScriptKind.JSX};
const out = {};
import {readFileSync} from 'fs';
for (const rel of paths) {
  const text = readFileSync(rel, 'utf8');
  const ext = rel.slice(rel.lastIndexOf('.'));
  const sf = ts.createSourceFile(rel, text, ts.ScriptTarget.Latest, true, kinds[ext] ?? ts.ScriptKind.JS);
  const found = new Map();
  const cb = (pos, end) => {
    if (end > pos) found.set(pos + ':' + end, {pos, end});
    return false;
  };
  const K = ts.SyntaxKind;
  const COMMENT = new Set([K.SingleLineCommentTrivia, K.MultiLineCommentTrivia]);
  const EXPR_BEFORE = new Set([K.EqualsToken, K.EqualsEqualsEqualsToken, K.ExclamationEqualsEqualsToken,
    K.EqualsGreaterThanToken, K.OpenParenToken, K.CommaToken, K.OpenBracketToken, K.ColonToken,
    K.QuestionToken, K.AmpersandAmpersandToken, K.BarBarToken, K.ReturnKeyword, K.CaseKeyword,
    K.InKeyword, K.OfKeyword, K.AwaitKeyword, K.YieldKeyword, K.DoKeyword, K.ElseKeyword,
    K.OpenBraceToken, K.CloseBraceToken, K.SemicolonToken]);
  const isJsx = ext === '.tsx' || ext === '.jsx';
  const variant = isJsx ? ts.LanguageVariant.JSX : ts.LanguageVariant.Standard;
  const variantLang = ts.LanguageVariant;

  const runCodePass = (record) => {
    const scanner = ts.createScanner(ts.ScriptTarget.Latest, false, variant, text);
    const templateStack = [];
    let prev = undefined;
    let token = scanner.scan();
    while (token !== K.EndOfFileToken && token !== K.Unknown) {
      if (COMMENT.has(token)) {
        record(scanner.getTokenPos(), scanner.getTextPos());
      } else if (token === K.TemplateHead || token === K.TemplateMiddle) {
        templateStack.push(0);
      } else if (token === K.OpenBraceToken && templateStack.length) {
        templateStack[templateStack.length - 1]++;
      } else if (token === K.CloseBraceToken && templateStack.length) {
        if (templateStack[templateStack.length - 1] === 0) {
          templateStack.pop();
          token = scanner.reScanTemplateToken(false);
          if (token === K.TemplateMiddle) templateStack.push(0);
          if (COMMENT.has(token)) record(scanner.getTokenPos(), scanner.getTextPos());
        } else {
          templateStack[templateStack.length - 1]--;
        }
      } else if (!isTrivia(token)) {
        prev = token;
      }
      token = scanner.scan();
    }
  };

  const isTrivia = (t) => t === K.WhitespaceTrivia || t === K.NewLineTrivia ||
    t === K.ShebangTrivia || t === K.ConflictMarkerTrivia || COMMENT.has(t);

  const jsxTextSpans = [];
  if (isJsx) {
    const spanWalk = (node) => {
      if (node.kind === K.JsxText) {
        jsxTextSpans.push([node.getStart(sf), node.getEnd()]);
      }
      ts.forEachChild(node, spanWalk);
    };
    spanWalk(sf);
  }
  const collect = (p, e) => { emit(p, e); return false; };
  const walk = (node) => {
    ts.forEachLeadingCommentRange(text, node.getFullStart(), collect);
    ts.forEachTrailingCommentRange(text, node.getEnd(), collect);
    const ot = node.operatorToken;
    if (ot !== undefined && ot.pos !== undefined) {
      ts.forEachLeadingCommentRange(text, ot.getFullStart(), collect);
    }
    ts.forEachChild(node, walk);
  };
  const overlapsJsxText = (p, e) => jsxTextSpans.some(([a, b]) => p < b && e > a);
  const inJsxText = (p) => jsxTextSpans.some(([a, b]) => p >= a && p < b);
  const emit = (p, e) => {
    if (!isJsx || !overlapsJsxText(p, e)) { cb(p, e); return; }
    let i = p;
    while (i < e) {
      const c1 = text.indexOf('//', i);
      const c2 = text.indexOf('/*', i);
      const c = c1 === -1 ? c2 : (c2 === -1 ? c1 : Math.min(c1, c2));
      if (c === -1 || c >= e) break;
      if (!inJsxText(c)) {
        let end;
        if (text[c + 1] === '/') {
          end = text.indexOf('\\n', c);
          if (end === -1) end = e;
        } else {
          end = text.indexOf('*/', c + 2);
          end = end === -1 ? e : end + 2;
        }
        cb(c, Math.min(end, e));
        i = end;
      } else {
        i = c + 2;
      }
    }
  };
  walk(sf);
  runCodePass(emit);
  out[rel] = [...found.values()].map(({pos, end}) => ({
    line: text.slice(0, pos).split('\\n').length,
    kind: text.slice(pos, pos + 2) === '/*' ? 'block' : 'line',
    text: text.slice(pos, end)}));
}
console.log(JSON.stringify({results: out}));
"""


def detect_ts_family(paths, repo_root):
    import tempfile
    node = os.environ.get("NODE", "node")
    candidates = []
    if os.environ.get("LOOM_TYPESCRIPT_DIR"):
        candidates.append(os.environ["LOOM_TYPESCRIPT_DIR"])
    candidates.append(os.path.join(repo_root, "ui", "node_modules", "typescript"))
    candidates.append(os.path.join(repo_root, "node_modules", "typescript"))
    with tempfile.NamedTemporaryFile("w", suffix=".mjs", delete=False) as f:
        f.write(TS_SCANNER_JS)
        script = f.name
    try:
        p = subprocess.run([node, script, json.dumps(candidates), json.dumps(paths)],
                           capture_output=True, text=True, timeout=300)
    finally:
        os.unlink(script)
    if p.returncode != 0:
        raise RuntimeError(f"ts scanner failed: {(p.stderr or p.stdout)[:400]}")
    payload = json.loads(p.stdout)
    if payload.get("error"):
        raise RuntimeError("ts scanner unavailable: " + payload["error"])
    return payload["results"]


def classify(path):
    base = os.path.basename(path)
    ext = os.path.splitext(base)[1].lower()
    if ext == ".md" or ext in (".markdown", ".mdown", ".mdx"):
        return "asset"
    if ext in (".csv", ".tsv", ".txt", ".log", ".gz", ".bin", ".fixture", ".png", ".jpg",
               ".jpeg", ".gif", ".ico", ".svg", ".woff", ".woff2", ".ttf", ".pdf"):
        return "asset"
    if base in ("evidence",) or "/evidence/" in path or "/playwright/" in path:
        return "asset"
    if ext == ".go":
        return "go"
    if ext in TS_FAMILY:
        return "ts"
    if ext == ".py":
        return "py"
    if ext == ".sh":
        return "sh"
    if ext in (".yaml", ".yml"):
        return "yaml"
    if ext == ".toml":
        return "toml"
    if ext in (".tla", ".cfg"):
        return "tla"
    if ext in (".graphql", ".graphqls", ".query"):
        return "graphql"
    if ext == ".json":
        return "json"
    if ext == ".ndjson":
        return "ndjson"
    if ext == ".html":
        return "html"
    if ext == ".sql":
        return "sql"
    if ext == ".css":
        return "css"
    if ext == ".tmpl":
        return "tmpl"
    if ext == ".mmd":
        return "mermaid"
    if ext == ".sum" or base == "go.sum":
        return "sum"
    if base == "go.mod":
        return "gomod"
    if base in SPECIAL_MAKE:
        return "makefile"
    if base in SPECIAL_IGNORE:
        return "ignore"
    if base.startswith("Dockerfile") or base.endswith(".dockerignore"):
        return "dockerfile" if base.startswith("Dockerfile") else "ignore"
    if base in SPECIAL_JSON:
        return "json"
    if base == ".editorconfig" or base == ".gitattributes":
        return "toml" if base == ".editorconfig" else "ignore"
    if base.endswith((".air.toml", ".air.dev.toml")):
        return "toml"
    if base == "nginx.conf":
        return "nginx"
    if "." not in base and base.islower():
        return "asset"
    return "UNSUPPORTED"


SPAN_BACKENDS = {
    "go": go_comment_spans,
    "sh": sh_comment_spans,
    "yaml": yaml_comment_spans,
    "toml": toml_comment_spans,
    "tla": tla_comment_spans,
    "graphql": graphql_comment_spans,
    "sql": sql_comment_spans,
    "css": css_comment_spans,
    "tmpl": tmpl_comment_spans,
    "py": py_comment_spans,
    "mermaid": lambda src: prefix_line_spans(src, ["%%", "#"]),
    "makefile": lambda src: prefix_line_spans(src, ["#"]),
    "dockerfile": lambda src: prefix_line_spans(src, ["#"]),
    "ignore": lambda src: prefix_line_spans(src, ["#"]),
    "nginx": lambda src: nginx_comment_spans(src),
}

PARSE_PROOFS = {
    "json": detect_json,
    "ndjson": detect_ndjson,
    "html": detect_html,
    "sum": detect_sum,
    "gomod": None,
}


def gomod_comments(src):
    return spans_to_comments(src, [
        (m.start(), m.end()) for m in re.finditer(r"//[^\n]*", src)
    ])


def nginx_comment_spans(src):
    spans = []
    i, n = 0, len(src)
    quote = None
    while i < n:
        ch = src[i]
        if quote:
            if ch == "\\" and quote == '"':
                i += 2
                continue
            if ch == quote:
                quote = None
            i += 1
            continue
        if ch in "\"'":
            quote = ch
        elif ch == "#":
            j = src.find("\n", i)
            spans.append((i, n if j < 0 else j))
            i = n if j < 0 else j
            continue
        i += 1
    return spans


def detect_file(path, src):
    backend = classify(path)
    if backend in ("asset", "UNSUPPORTED"):
        return backend, [], None
    if backend == "gomod":
        return backend, gomod_comments(src), None
    if backend in PARSE_PROOFS:
        try:
            return backend, PARSE_PROOFS[backend](path, src), None
        except Exception as exc:
            return backend, [], str(exc)
    if backend == "ts":
        return backend, None, "batch-required"
    try:
        spans = SPAN_BACKENDS[backend](src)
    except Exception as exc:
        return backend, [], str(exc)
    return backend, spans_to_comments(src, spans), None


def allowlist_category(backend, comment, line_text, path, provenance_ledger,
                       ledger_bound, governance_directives=None):
    text = comment["text"]
    if path in provenance_ledger:
        return "generator-provenance"
    for entry in (governance_directives or {}).get(path, []):
        if entry["pattern"].search(text):
            return "governance-directive"
    if path in ledger_bound:
        return "ledger-bound-pinned"
    if backend in ("sh", "py", "toml", "makefile", "ignore", "mermaid", "go") \
            and comment["line"] == 1 and text.startswith("#!"):
        return "execution-shebang"
    if backend == "go" and GO_DIRECTIVE.match(text):
        return "go-compiler-directive"
    if backend == "ts" and JS_DIRECTIVE.match(text):
        return "js-tool-directive"
    if backend == "gomod" and GOMOD_INDIRECT.match(text):
        return "go-tool-managed-marker"
    if backend == "py" and PY_DIRECTIVE.match(text):
        return "python-tool-pragma"
    if backend == "sh" and SHELL_KEEP.match(text[1:]):
        return "shell-tool-directive"
    if backend == "dockerfile" and DOCKERFILE_DIRECTIVE.match(text):
        return "dockerfile-parser-directive"
    return None
