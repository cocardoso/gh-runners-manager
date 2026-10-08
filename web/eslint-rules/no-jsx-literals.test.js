import { RuleTester } from "eslint";
import tseslint from "typescript-eslint";
import rule from "./no-jsx-literals.js";

const tester = new RuleTester({ languageOptions: { parser: tseslint.parser, parserOptions: { ecmaFeatures: { jsx: true } } } });

tester.run("no-jsx-literals", rule, {
  valid: [
    { code: "<p>{t('a.b')}</p>" },
    { code: "<p> · — 12 </p>" },
    { code: '<img alt="" />' },
    { code: '<Input placeholder="ubuntu-slim" />', options: [{ allow: ["ubuntu-slim"] }] },
    { code: "<span>Proxmox</span>", options: [{ allow: ["Proxmox"] }] },
  ],
  invalid: [
    { code: "<p>Save</p>", errors: 1 },
    { code: '<Input label="Name" />', errors: 1 },
    { code: "<Button aria-label={`Remove ${name}`} />", errors: 1 },
    { code: '<span title={"Hello"} />', errors: 1 },
  ],
});
