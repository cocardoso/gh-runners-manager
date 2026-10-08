import js from "@eslint/js";
import globals from "globals";
import reactHooks from "eslint-plugin-react-hooks";
import tseslint from "typescript-eslint";
import noJsxLiterals from "./eslint-rules/no-jsx-literals.js";

export default tseslint.config(
  { ignores: ["dist", "src/api/schema.d.ts", "src/blocks"] },
  {
    extends: [js.configs.recommended, ...tseslint.configs.recommended],
    files: ["**/*.{ts,tsx}"],
    languageOptions: { ecmaVersion: 2022, globals: globals.browser },
    plugins: { "react-hooks": reactHooks },
    rules: { ...reactHooks.configs.recommended.rules },
  },
  {
    // Visible text comes from the i18n dictionaries (src/i18n).
    files: ["src/**/*.tsx"],
    ignores: ["src/**/*.test.tsx", "src/test/**"],
    plugins: { local: { rules: { "no-jsx-literals": noJsxLiterals } } },
    rules: {
      "local/no-jsx-literals": [
        "error",
        // The same in every language: product names, acronyms and code examples.
        { allow: ["gh-runners-manager", "Proxmox", "ubuntu-slim", "CPU", "setup-token", "runs-on: <name>", "https://github.com/owner/repo", "linux, big"] },
      ],
    },
  },
);
