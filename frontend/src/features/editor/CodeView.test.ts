import { EditorSelection, EditorState, type TransactionSpec } from "@codemirror/state";
import type { EditorView } from "@codemirror/view";
import { describe, expect, it } from "vitest";
import { insertIndent } from "./CodeView";

// The command only reads state and dispatches, so a view needs nothing else.
function press(doc: string, selection: EditorSelection, readOnly = false) {
  let state = EditorState.create({
    doc,
    selection,
    extensions: [EditorState.readOnly.of(readOnly), EditorState.allowMultipleSelections.of(true)],
  });
  const view = {
    get state() {
      return state;
    },
    dispatch(spec: TransactionSpec) {
      state = state.update(spec).state;
    },
  } as unknown as EditorView;
  const handled = insertIndent(view);
  return { handled, doc: state.doc.toString(), head: state.selection.main.head };
}

describe("insertIndent", () => {
  it("inserts spaces, never a tab character", () => {
    expect(press("a:", EditorSelection.single(0))).toEqual({ handled: true, doc: "  a:", head: 2 });
  });

  it("stops at the next indent column", () => {
    expect(press("a: b", EditorSelection.single(3)).doc).toBe("a:  b");
  });

  it("indents every selected line", () => {
    expect(press("a:\nb:", EditorSelection.single(0, 5)).doc).toBe("  a:\n  b:");
  });

  it("leaves a read-only view alone", () => {
    expect(press("a:", EditorSelection.single(0), true)).toEqual({ handled: false, doc: "a:", head: 0 });
  });
});
