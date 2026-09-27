const js = require("@eslint/js");

module.exports = [
  {
    ...js.configs.recommended,
    files: ["src/**/*.js"],
    languageOptions: {
      ecmaVersion: 2020,
      sourceType: "module",
      globals: {
        window: "readonly",
        document: "readonly",
        localStorage: "readonly",
        location: "readonly",
        console: "readonly",
        navigator: "readonly",
        HTMLElement: "readonly",
      },
    },
    rules: {
      "no-unused-vars": "off",
    },
  },
  {
    ignores: ["src/**/*.vue"],
  },
];
