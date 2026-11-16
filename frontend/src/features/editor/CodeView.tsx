import { json } from "@codemirror/lang-json";
import { indentLess, indentMore } from "@codemirror/commands";
import { getIndentUnit, HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { yaml } from "@codemirror/lang-yaml";
import { unifiedMergeView } from "@codemirror/merge";
import { EditorSelection, EditorState } from "@codemirror/state";
import { type Command, EditorView, keymap } from "@codemirror/view";
import { tags } from "@lezer/highlight";
import { basicSetup } from "codemirror";
import { useEffect, useRef } from "react";

const theme = EditorView.theme({
  "&": { height: "100%", backgroundColor: "var(--ground)", color: "var(--ink)" },
  ".cm-scroller": {
    fontFamily: '"Atkinson Hyperlegible Mono", ui-monospace, monospace',
    fontSize: "12px",
    lineHeight: "1.5",
  },
  ".cm-gutters": { backgroundColor: "var(--panel)", color: "var(--muted)", border: "none" },
  ".cm-activeLine, .cm-activeLineGutter": { backgroundColor: "transparent" },
  ".cm-cursor": { borderLeftColor: "var(--ink)" },
  "&.cm-focused .cm-selectionBackground, .cm-selectionBackground": {
    backgroundColor: "var(--raised)",
  },
});

const highlighting = syntaxHighlighting(
  HighlightStyle.define([
    { tag: [tags.propertyName, tags.definition(tags.propertyName)], color: "var(--focus)" },
    { tag: [tags.number, tags.bool, tags.null], color: "var(--warn)" },
    { tag: tags.comment, color: "var(--muted)" },
  ]),
);

// Tab inserts spaces up to the next indent stop, because YAML forbids tab
// characters for indentation. With a selection it indents the lines instead.
// basicSetup leaves Tab unbound so it moves focus; Escape then Tab still does.
export const insertIndent: Command = (view) => {
  const { state } = view;
  if (state.readOnly) return false;
  if (state.selection.ranges.some((r) => !r.empty)) return indentMore(view);
  const unit = getIndentUnit(state);
  view.dispatch(
    state.changeByRange((range) => {
      const column = range.head - state.doc.lineAt(range.head).from;
      const spaces = " ".repeat(unit - (column % unit));
      return {
        changes: { from: range.head, insert: spaces },
        range: EditorSelection.cursor(range.head + spaces.length),
      };
    }),
  );
  return true;
};

const tabKeys = keymap.of([{ key: "Tab", run: insertIndent, shift: indentLess }]);

interface Props {
  value: string;
  // Omit for a read-only view.
  onChange?: (value: string) => void;
  // Shows value as a diff against this text.
  original?: string;
  language?: "yaml" | "json";
}

export function CodeView({ value, onChange, original, language = "yaml" }: Props) {
  const host = useRef<HTMLDivElement>(null);
  const changeHandler = useRef(onChange);
  changeHandler.current = onChange;
  const editable = onChange !== undefined;

  // The editor owns its text after creation, so value only seeds it. Callers
  // use a React key to load different content.
  useEffect(() => {
    const view = new EditorView({
      parent: host.current!,
      state: EditorState.create({
        doc: value,
        extensions: [
          basicSetup,
          ...(editable ? [tabKeys] : []),
          // Long values are the norm in JSON, where text can't span lines.
          ...(language === "json" ? [json(), EditorView.lineWrapping] : [yaml()]),
          theme,
          highlighting,
          EditorState.readOnly.of(!editable),
          EditorView.editable.of(editable),
          EditorView.updateListener.of((update) => {
            if (update.docChanged) {
              changeHandler.current?.(update.state.doc.toString());
            }
          }),
          ...(original === undefined
            ? []
            : [unifiedMergeView({ original, mergeControls: false, gutter: true })]),
        ],
      }),
    });
    return () => view.destroy();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return <div className="code-view" ref={host} data-testid="code-view" />;
}
