// Visible text belongs in the i18n dictionaries (src/i18n): this rule reports English
// (any letters) written straight into JSX text or into the attributes people read.
const ATTRS = new Set(["label", "title", "description", "placeholder", "aria-label", "legend", "content", "alt", "hint", "customValue"]);
const LETTER = /\p{L}{2,}/u;

export default {
  meta: {
    type: "problem",
    messages: { literal: "Visible text belongs in the i18n dictionaries: {{text}}" },
    // allow: texts that are the same in every language (product names, code examples).
    schema: [{ type: "object", properties: { allow: { type: "array", items: { type: "string" } } }, additionalProperties: false }],
  },
  create(context) {
    const allow = new Set(context.options[0]?.allow ?? []);
    const report = (node, text) => {
      if (allow.has(text.trim())) return;
      context.report({ node, messageId: "literal", data: { text: text.trim().slice(0, 40) } });
    };
    return {
      JSXText(node) {
        if (LETTER.test(node.value)) report(node, node.value);
      },
      JSXAttribute(node) {
        if (!ATTRS.has(node.name.name) || !node.value) return;
        const v = node.value;
        if (v.type === "Literal" && typeof v.value === "string" && LETTER.test(v.value)) report(v, v.value);
        if (v.type === "JSXExpressionContainer") {
          const e = v.expression;
          if (e.type === "Literal" && typeof e.value === "string" && LETTER.test(e.value)) report(e, e.value);
          if (e.type === "TemplateLiteral" && e.quasis.some((q) => LETTER.test(q.value.cooked ?? ""))) report(e, e.quasis.map((q) => q.value.cooked).join("…"));
        }
      },
    };
  },
};
