import { describe, expect, it } from "vitest";
import {
  applyValues,
  dataKind,
  formatValues,
  keptKeys,
  parseObject,
  printObject,
  valuesFromYaml,
} from "./dataValues";

const bytesBase64 = (bytes: Iterable<number>) => btoa(String.fromCharCode(...bytes));
const base64 = (text: string) => bytesBase64(new TextEncoder().encode(text));

function configMap(data: Record<string, string>): string {
  return JSON.stringify({ apiVersion: "v1", kind: "ConfigMap", metadata: { name: "c" }, data });
}

function secret(data: Record<string, string>): string {
  return JSON.stringify({ apiVersion: "v1", kind: "Secret", metadata: { name: "s" }, data });
}

// The JSON view of an object, sent back untouched.
function roundTrip(yamlText: string, kind: "configmap" | "secret") {
  const { values } = valuesFromYaml(yamlText, kind);
  return applyValues(yamlText, formatValues(values), kind).data;
}

describe("dataKind", () => {
  it("matches only core ConfigMaps and Secrets", () => {
    expect(dataKind({ group: "", resource: "configmaps" })).toBe("configmap");
    expect(dataKind({ group: "", resource: "secrets" })).toBe("secret");
    expect(dataKind({ group: "", resource: "pods" })).toBeUndefined();
    expect(dataKind({ group: "example.com", resource: "secrets" })).toBeUndefined();
  });
});

describe("reading values", () => {
  it("decodes Secret text", () => {
    const text = secret({ password: base64("hunter2"), thai: base64("สวัสดี") });
    expect(valuesFromYaml(text, "secret").values).toEqual({ password: "hunter2", thai: "สวัสดี" });
  });

  it("expands JSON text into nested values", () => {
    const text = configMap({ "app.json": '{"debug":true,"ports":[80]}', plain: "8080" });
    expect(valuesFromYaml(text, "configmap").values).toEqual({
      "app.json": { debug: true, ports: [80] },
      plain: "8080",
    });
  });

  it.each([
    ["a number JavaScript would round", '{"id":12345678901234567890}'],
    ["a repeated key", '{"a":1,"a":2}'],
    ["keys JavaScript would reorder", '{"2":"b","1":"a"}'],
    ["a decimal that would lose its zero", '{"ratio":1.0}'],
    ["broken JSON", '{"a":'],
  ])("leaves %s as text", (_, value) => {
    expect(valuesFromYaml(configMap({ value }), "configmap").values.value).toBe(value);
  });

  it("lists binary values apart", () => {
    const bytes = bytesBase64([0xff, 0xfe, 0x00]);
    const result = valuesFromYaml(secret({ cert: bytes, name: base64("a") }), "secret");
    expect(result.values).toEqual({ name: "a" });
    expect(result.binary).toEqual([{ key: "cert", bytes: 3 }]);
  });

  it("lists ConfigMap binaryData apart", () => {
    const text = JSON.stringify({ kind: "ConfigMap", binaryData: { blob: base64("abc") } });
    expect(valuesFromYaml(text, "configmap").binary).toEqual([{ key: "blob", bytes: 3 }]);
  });

  it("treats a missing data field as empty", () => {
    expect(valuesFromYaml(JSON.stringify({ kind: "Secret" }), "secret").values).toEqual({});
  });
});

describe("applying values", () => {
  it.each([
    ["spaced JSON", '{\n    "a" : 1\n}\n'],
    ["minified JSON", '{"a":1}'],
    ["escaped JSON", '{"path":"a\\/b"}'],
    ["a trailing newline", "text\n"],
    ["an empty value", ""],
  ])("keeps %s exactly when nothing was edited", (_, value) => {
    expect(roundTrip(configMap({ value }), "configmap")).toEqual({ value });
    const encoded = base64(value);
    expect(roundTrip(secret({ value: encoded }), "secret")).toEqual({ value: encoded });
  });

  it("keeps a byte order mark", () => {
    const encoded = bytesBase64([0xef, 0xbb, 0xbf, 0x61]);
    expect(roundTrip(secret({ value: encoded }), "secret")).toEqual({ value: encoded });
  });

  it("encodes edited Secret values and leaves the others", () => {
    const text = secret({ a: base64("old"), b: base64("same") });
    const result = applyValues(text, '{"a":"ใหม่","b":"same","c":"added"}', "secret");
    expect(result.data).toEqual({ a: base64("ใหม่"), b: base64("same"), c: base64("added") });
  });

  it("removes deleted keys", () => {
    const result = applyValues(configMap({ a: "1", b: "2" }), '{"a":"1"}', "configmap");
    expect(result.data).toEqual({ a: "1" });
  });

  it("leaves out data when no keys remain", () => {
    expect(applyValues(configMap({ a: "1" }), "{}", "configmap")).not.toHaveProperty("data");
  });

  it("keeps the rest of the object", () => {
    const result = applyValues(configMap({ a: "1" }), '{"a":"2"}', "configmap");
    expect(result).toMatchObject({ apiVersion: "v1", kind: "ConfigMap", metadata: { name: "c" } });
  });

  it("writes an edited nested value in the shape it had", () => {
    const text = configMap({ flat: '{"a":1}', tall: '{\n  "a": 1\n}' });
    const result = applyValues(text, '{"flat":{"a":2},"tall":{"a":2}}', "configmap");
    expect(result.data).toEqual({ flat: '{"a":2}', tall: '{\n  "a": 2\n}' });
  });

  it("turns numbers and booleans into text", () => {
    const result = applyValues(configMap({}), '{"port":8080,"on":true}', "configmap");
    expect(result.data).toEqual({ port: "8080", on: "true" });
  });

  it("keeps binary values", () => {
    const bytes = bytesBase64([0xff, 0xfe]);
    const result = applyValues(secret({ cert: bytes, a: base64("1") }), '{"a":"2"}', "secret");
    expect(result.data).toEqual({ cert: bytes, a: base64("2") });
  });

  it("keeps ConfigMap binaryData out of data", () => {
    const text = JSON.stringify({ kind: "ConfigMap", binaryData: { blob: "YWJj" } });
    const result = applyValues(text, '{"a":"1"}', "configmap");
    expect(result.data).toEqual({ a: "1" });
    expect(result.binaryData).toEqual({ blob: "YWJj" });
  });

  it.each([
    ["broken JSON", '{"a":', /valid JSON/],
    ["an array", "[1]", /one JSON object/],
    ["null", "null", /one JSON object/],
    ["a null value", '{"a":null}', /is null/],
  ])("rejects %s", (_, json, message) => {
    expect(() => applyValues(configMap({}), json, "configmap")).toThrow(message);
  });

  it("rejects reusing a binary key", () => {
    const bytes = bytesBase64([0xff]);
    expect(() => applyValues(secret({ cert: bytes }), '{"cert":"x"}', "secret")).toThrow(/binary/);
  });
});

describe("keptKeys", () => {
  it("lists removed keys that are still in the result", () => {
    const after = configMap({ a: "1", b: "2", c: "3" });
    expect(keptKeys(after, '{"a":"1","c":"3"}', "configmap")).toEqual(["b"]);
  });

  it("ignores keys that really went, and binary values", () => {
    const cert = bytesBase64([0xff]);
    expect(keptKeys(secret({ a: base64("1"), cert }), '{"a":"1"}', "secret")).toEqual([]);
  });
});

describe("switching to YAML", () => {
  it.each(["yes", "no", "on", "off", "~", "null", "1e3", "0x10", "012", "1_000", "12:30:00", "true"])(
    "keeps %s as a string",
    (value) => {
      const printed = printObject({ data: { value } });
      expect(parseObject(printed)).toEqual({ data: { value } });
      expect(printed).toMatch(/["']/);
    },
  );
});
