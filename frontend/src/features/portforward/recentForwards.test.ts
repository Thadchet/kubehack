import { describe, expect, it } from "vitest";
import { initialPort, rememberedLocalPort } from "./recentForwards";

const recent = [
  { port: 9090, localPort: 19090 },
  { port: 80, localPort: 8080 },
];

describe("initialPort", () => {
  it("prefers the port forwarded last", () => {
    expect(initialPort(recent, [80, 9090])).toBe("9090");
  });

  it("falls back to the first declared port", () => {
    expect(initialPort([], [80, 9090])).toBe("80");
    expect(initialPort([], [])).toBe("");
  });
});

describe("rememberedLocalPort", () => {
  it("finds the local port used for that port", () => {
    expect(rememberedLocalPort(recent, "80")).toBe("8080");
    expect(rememberedLocalPort(recent, " 9090 ")).toBe("19090");
  });

  it("is empty for a port never forwarded", () => {
    expect(rememberedLocalPort(recent, "443")).toBe("");
  });
});
