import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  clearConfig,
  loadConfig,
  saveConfig,
  setLastVersion,
  type DashConfig,
} from "./config.js";

const SAMPLE: DashConfig = {
  baseUrl: "https://loadoutd.example.internal",
  token: "test-token-not-real",
  identity: "AGE-SECRET-KEY-1TESTDUMMYVALUEDONOTUSEXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
  deviceName: "test-browser",
  lastVersion: "v0",
};

describe("loadConfig", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("returns null when nothing is stored", () => {
    expect(loadConfig()).toBeNull();
  });
});

describe("saveConfig and loadConfig", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("round-trips a full config", () => {
    saveConfig(SAMPLE);
    expect(loadConfig()).toEqual(SAMPLE);
  });
});

describe("setLastVersion", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("updates only the version field of an existing config", () => {
    saveConfig(SAMPLE);
    setLastVersion("v7");
    expect(loadConfig()).toEqual({ ...SAMPLE, lastVersion: "v7" });
  });

  it("does nothing when no config is stored yet", () => {
    setLastVersion("v7");
    expect(loadConfig()).toBeNull();
  });
});

describe("clearConfig", () => {
  beforeEach(() => {
    window.localStorage.clear();
  });

  it("empties the stored config", () => {
    saveConfig(SAMPLE);
    clearConfig();
    expect(loadConfig()).toBeNull();
  });
});

describe("when localStorage throws", () => {
  let original: Storage;

  beforeEach(() => {
    original = window.localStorage;
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        getItem(): never {
          throw new Error("storage blocked");
        },
        setItem(): never {
          throw new Error("storage blocked");
        },
        removeItem(): never {
          throw new Error("storage blocked");
        },
      },
    });
  });

  afterEach(() => {
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: original,
    });
  });

  it("does not throw from loadConfig, and returns null", () => {
    expect(() => loadConfig()).not.toThrow();
    expect(loadConfig()).toBeNull();
  });

  it("does not throw from saveConfig", () => {
    expect(() => saveConfig(SAMPLE)).not.toThrow();
  });
});
