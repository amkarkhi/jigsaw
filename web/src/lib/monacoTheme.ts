// Define a Monaco theme so the suggestion widget, hover, and editor surfaces
// all use our palette. Setting these via `defineTheme` is more reliable than
// CSS overrides because Monaco computes inline styles from theme colors and
// those win against any external CSS short of !important rules in every
// nested selector.
//
// Use via the @monaco-editor/react `beforeMount` prop:
//
//   <Editor beforeMount={defineJigsawTheme} theme="jigsaw-dark" ... />

import type { Monaco } from "@monaco-editor/react";

export const JIGSAW_THEME = "jigsaw-dark";

export function defineJigsawTheme(monaco: Monaco) {
  monaco.editor.defineTheme(JIGSAW_THEME, {
    base: "vs-dark",
    inherit: true,
    rules: [],
    colors: {
      "editor.background":                          "#0b0d12",
      "editor.foreground":                          "#d6dbe4",
      "editor.lineHighlightBackground":             "#161a22",
      "editorLineNumber.foreground":                "#3d4654",
      "editorLineNumber.activeForeground":          "#7cf0c7",
      "editorCursor.foreground":                    "#7cf0c7",
      "editor.selectionBackground":                 "#2e8c6c66",

      // The suggest widget — the popup Ctrl-Space brings up.
      "editorSuggestWidget.background":             "#11141a",
      "editorSuggestWidget.foreground":             "#d6dbe4",
      "editorSuggestWidget.border":                 "#2a3140",
      "editorSuggestWidget.selectedBackground":     "#2e8c6c",
      "editorSuggestWidget.selectedForeground":     "#ffffff",
      "editorSuggestWidget.highlightForeground":    "#7cf0c7",
      "editorSuggestWidget.focusHighlightForeground": "#fff79a",

      // Hover and parameter hints — same family.
      "editorHoverWidget.background":               "#11141a",
      "editorHoverWidget.foreground":               "#d6dbe4",
      "editorHoverWidget.border":                   "#2a3140",
      "editorWidget.background":                    "#11141a",
      "editorWidget.foreground":                    "#d6dbe4",
      "editorWidget.border":                        "#2a3140",

      // Quick input (Ctrl-P palette) — also uses widget colors.
      "list.hoverBackground":                       "#161a22",
      "list.activeSelectionBackground":             "#2e8c6c",
      "list.activeSelectionForeground":             "#ffffff",
      "list.focusBackground":                       "#2e8c6c",
      "list.focusForeground":                       "#ffffff",
    },
  });
}
