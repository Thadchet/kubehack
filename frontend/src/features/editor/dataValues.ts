import { parse, stringify } from "yaml";

// The API server reads YAML 1.1, where an unquoted yes is a boolean. Writing the
// same version keeps such strings quoted.
const yamlOptions = { version: "1.1", lineWidth: 0 } as const;

export type DataKind = "configmap" | "secret";

export interface BinaryValue {
  key: string;
  bytes: number;
}

export interface DataValues {
  values: Record<string, unknown>;
  // Bytes that aren't text can't be shown or edited as JSON.
  binary: BinaryValue[];
}

type Fields = Record<string, unknown>;

export function dataKind(resource: { group: string; resource: string }): DataKind | undefined {
  if (resource.group !== "") {
    return undefined;
  }
  return resource.resource === "configmaps"
    ? "configmap"
    : resource.resource === "secrets"
      ? "secret"
      : undefined;
}

function base64Bytes(encoded: string): Uint8Array {
  return Uint8Array.from(atob(encoded), (character) => character.charCodeAt(0));
}

function encodeBase64(text: string): string {
  let binary = "";
  for (const byte of new TextEncoder().encode(text)) {
    binary += String.fromCharCode(byte);
  }
  return btoa(binary);
}

function decodeText(bytes: Uint8Array): string | undefined {
  try {
    // Keeping a byte order mark means the text encodes back to the same bytes.
    return new TextDecoder("utf-8", { fatal: true, ignoreBOM: true }).decode(bytes);
  } catch {
    return undefined;
  }
}

function withoutSpacing(json: string): string {
  return json.replace(/"(?:[^"\\]|\\.)*"|\s+/g, (match) => (match[0] === '"' ? match : ""));
}

// Expands text only if parsing it loses nothing, because the value is rebuilt from
// the parsed form when edited. A number too large for JavaScript, a repeated key
// or a reordered key would otherwise change without anyone touching it.
function expandJson(text: string): unknown {
  if (!/^\s*[[{]/.test(text)) {
    return text;
  }
  try {
    const parsed: unknown = JSON.parse(text);
    return JSON.stringify(parsed) === withoutSpacing(text) ? parsed : text;
  } catch {
    return text;
  }
}

function fieldsOf(value: unknown): Fields {
  return value !== null && typeof value === "object" ? (value as Fields) : {};
}

function textOf(key: string, value: unknown): string {
  if (typeof value !== "string") {
    throw new Error(`The value of ${key} isn't text. Put it in quotes.`);
  }
  return value;
}

// originalText returns a key's current text, or undefined for binary or missing.
function originalText(kind: DataKind, encoded: unknown): string | undefined {
  if (typeof encoded !== "string") {
    return undefined;
  }
  return kind === "secret" ? decodeText(base64Bytes(encoded)) : encoded;
}

export function readValues(object: unknown, kind: DataKind): DataValues {
  const fields = fieldsOf(object);
  const values: Fields = {};
  const binary: BinaryValue[] = [];
  for (const [key, value] of Object.entries(fieldsOf(fields.data))) {
    const encoded = textOf(key, value);
    if (kind === "configmap") {
      values[key] = expandJson(encoded);
      continue;
    }
    const bytes = base64Bytes(encoded);
    const text = decodeText(bytes);
    if (text === undefined) {
      binary.push({ key, bytes: bytes.length });
    } else {
      values[key] = expandJson(text);
    }
  }
  for (const [key, value] of Object.entries(fieldsOf(fields.binaryData))) {
    binary.push({ key, bytes: base64Bytes(textOf(key, value)).length });
  }
  return { values, binary };
}

export function formatValues(values: Record<string, unknown>): string {
  return `${JSON.stringify(values, null, 2)}\n`;
}

// displayValue shows one key's value for reading: text as it is stored, and
// expanded JSON as YAML.
export function displayValue(value: unknown): string {
  if (typeof value === "string") {
    return value;
  }
  return stringify(value, yamlOptions).replace(/\n$/, "");
}

export function parseObject(yamlText: string): unknown {
  return parse(yamlText, yamlOptions);
}

export function printObject(object: unknown): string {
  return stringify(object, yamlOptions);
}

export function valuesFromYaml(yamlText: string, kind: DataKind): DataValues {
  return readValues(parseObject(yamlText), kind);
}

function editedText(key: string, value: unknown, original: string | undefined): string {
  if (value === null) {
    throw new Error(`The value of ${key} is null. Use "" for an empty value.`);
  }
  if (typeof value === "string") {
    return value;
  }
  if (typeof value !== "object") {
    return String(value);
  }
  if (original !== undefined) {
    const before = expandJson(original);
    if (JSON.stringify(before) === JSON.stringify(value)) {
      return original;
    }
  }
  // Text stored on one line stays on one line.
  const compact = original !== undefined && !original.includes("\n");
  return JSON.stringify(value, null, compact ? 0 : 2);
}

function parseEdited(jsonText: string): Fields {
  let edited: unknown;
  try {
    edited = JSON.parse(jsonText);
  } catch (error) {
    throw new Error(`This isn't valid JSON. ${(error as Error).message}`);
  }
  if (edited === null || typeof edited !== "object" || Array.isArray(edited)) {
    throw new Error("Values must be one JSON object of keys and values.");
  }
  return edited as Fields;
}

// keptKeys lists keys the edit removed that are still in the result, which is what
// server-side apply does with fields another tool manages.
export function keptKeys(afterYaml: string, jsonText: string, kind: DataKind): string[] {
  const edited = parseEdited(jsonText);
  return Object.keys(valuesFromYaml(afterYaml, kind).values).filter((key) => !(key in edited));
}

// applyValues returns the whole object with its data replaced. A value that was
// not edited keeps its exact stored form, whatever the JSON view made of it.
export function applyValues(yamlText: string, jsonText: string, kind: DataKind): Fields {
  const object = { ...fieldsOf(parseObject(yamlText)) };
  const stored = fieldsOf(object.data);
  const { binary } = readValues(object, kind);
  const edited = parseEdited(jsonText);

  const data: Fields = {};
  for (const item of binary) {
    if (item.key in edited) {
      throw new Error(`${item.key} already holds binary data. Choose another key.`);
    }
    if (item.key in stored) {
      data[item.key] = stored[item.key];
    }
  }
  for (const [key, value] of Object.entries(edited)) {
    const original = originalText(kind, stored[key]);
    const text = editedText(key, value, original);
    if (text === original) {
      data[key] = stored[key];
    } else {
      data[key] = kind === "secret" ? encodeBase64(text) : text;
    }
  }

  if (Object.keys(data).length > 0) {
    object.data = data;
  } else {
    delete object.data;
  }
  return object;
}
