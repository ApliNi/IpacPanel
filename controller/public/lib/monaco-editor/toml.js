define("vs/toml", ["exports"], (function(e) {
  "use strict";
  const conf = {
    comments: { lineComment: "#" },
    brackets: [["{", "}"], ["[", "]"], ["(", ")"]],
    autoClosingPairs: [
      { open: "{", close: "}" },
      { open: "[", close: "]" },
      { open: "(", close: ")" },
      { open: '"', close: '"' },
      { open: "'", close: "'" }
    ],
    surroundingPairs: [
      { open: "{", close: "}" },
      { open: "[", close: "]" },
      { open: "(", close: ")" },
      { open: '"', close: '"' },
      { open: "'", close: "'" }
    ],
    folding: {
      markers: {
        start: new RegExp("^\\s*\\[\\[?[^\\]]+\\]\\]?"),
        end: new RegExp("^\\s*(?:\\[\\[?[^\\]]+\\]\\]?|$)")
      }
    }
  };

  const language = {
    defaultToken: "",
    tokenPostfix: ".toml",
    keywords: ["true", "false", "inf", "nan"],
    escapes: /\\(?:[abfnrtv\\"']|x[0-9A-Fa-f]{2}|u[0-9A-Fa-f]{4}|U[0-9A-Fa-f]{8})/,
    tokenizer: {
      root: [
        { include: "@whitespace" },
        [/^\s*\[\[[^\]]+\]\]/, "metatag"],
        [/^\s*\[[^\]]+\]/, "metatag"],
        [/([A-Za-z0-9_-]+)(?=\s*=)/, "key"],
        [/"([^"\\]|\\.)*"(?=\s*=)/, "key"],
        [/'[^']*'(?=\s*=)/, "key"],
        [/=/, "delimiter"],
        [/\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})?)?/, "type"],
        [/\d{2}:\d{2}:\d{2}(?:\.\d+)?/, "type"],
        [/0x[0-9a-fA-F_]+/, "number.hex"],
        [/0o[0-7_]+/, "number.octal"],
        [/0b[01_]+/, "number.binary"],
        [/[+-]?(?:0|[1-9](?:_?\d)*)(?:\.(?:0|[1-9](?:_?\d)*))?(?:[eE][+-]?(?:0|[1-9](?:_?\d)*))?/, "number"],
        [/\b(?:true|false)\b/, "keyword"],
        [/\b(?:inf|nan)\b/, "number"],
        [/"""/, "string", "@multiBasicString"],
        [/'''/, "string", "@multiLiteralString"],
        [/"([^"\\]|\\.)*$/, "string.invalid"],
        [/'[^']*$/, "string.invalid"],
        [/"/, "string", "@basicString"],
        [/'/, "string", "@literalString"],
        [/[{}()\[\],.]/, "delimiter"]
      ],
      whitespace: [
        [/[ \t\r\n]+/, ""],
        [/#.*$/, "comment"]
      ],
      basicString: [
        [/[^"\\]+/, "string"],
        [/@escapes/, "string.escape"],
        [/\\./, "string.escape.invalid"],
        [/"/, "string", "@pop"]
      ],
      literalString: [
        [/[^']+/, "string"],
        [/'/, "string", "@pop"]
      ],
      multiBasicString: [
        [/[^"\\]+/, "string"],
        [/@escapes/, "string.escape"],
        [/\\./, "string.escape.invalid"],
        [/"""/, "string", "@pop"],
        [/"/, "string"]
      ],
      multiLiteralString: [
        [/[^']+/, "string"],
        [/'''/, "string", "@pop"],
        [/'/, "string"]
      ]
    }
  };

  e.conf = conf;
  e.language = language;
  Object.defineProperty(e, Symbol.toStringTag, { value: "Module" });
}));
