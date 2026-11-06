import { json } from "@codemirror/lang-json";
import { HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { yaml } from "@codemirror/lang-yaml";
import { unifiedMergeView } from "@codemirror/merge";
import { EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
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
