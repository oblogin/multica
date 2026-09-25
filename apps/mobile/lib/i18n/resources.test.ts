// @vitest-environment node
import fs from "node:fs";
import path from "node:path";
import { describe, expect, it } from "vitest";
import { SUPPORTED_LOCALES } from "./types";

const LOCALES_ROOT = path.resolve(__dirname, "../../locales");

function readLocale(locale: string): [string, unknown][] {
  return fs
    .readdirSync(path.join(LOCALES_ROOT, locale))
    .filter((file) => file.endsWith(".json"))
    .map((file): [string, unknown] => [
      file,
      JSON.parse(
        fs.readFileSync(path.join(LOCALES_ROOT, locale, file), "utf8"),
      ),
    ]);
}

function flatten(value: unknown, prefix = ""): Set<string> {
  const keys = new Set<string>();
  if (typeof value !== "object" || value === null) {
    keys.add(prefix);
    return keys;
  }
  for (const [key, child] of Object.entries(value)) {
    const nextKey = prefix ? `${prefix}.${key}` : key;
    for (const childKey of flatten(child, nextKey)) keys.add(childKey);
  }
  return keys;
}

function leafValues(value: unknown, prefix = ""): Map<string, string> {
  const values = new Map<string, string>();
  if (typeof value === "string") {
    values.set(prefix, value);
    return values;
  }
  if (typeof value !== "object" || value === null) return values;
  for (const [key, child] of Object.entries(value)) {
    const nextKey = prefix ? `${prefix}.${key}` : key;
    for (const [childKey, text] of leafValues(child, nextKey)) {
      values.set(childKey, text);
    }
  }
  return values;
}

function placeholders(value: string): string[] {
  return [...value.matchAll(/{{\s*([^{}]+?)\s*}}/g)]
    .map((match) => match[1])
    .sort();
}

describe("mobile i18n resources", () => {
  for (const locale of SUPPORTED_LOCALES.filter((value) => value !== "en")) {
    it(`keeps ${locale} namespaces and keys aligned with English`, () => {
      const enResources = readLocale("en");
      const translatedResources = new Map(readLocale(locale));
      expect([...translatedResources.keys()].sort()).toEqual(
        enResources.map(([namespace]) => namespace).sort(),
      );

      for (const [namespace, value] of enResources) {
        const translatedValue = translatedResources.get(namespace);
        const enKeys = flatten(value);
        const translatedKeys = flatten(translatedValue);
        // Chinese does not distinguish grammatical plural forms.
        const missing = [...enKeys].filter((key) => {
          if (translatedKeys.has(key)) return false;
          return !(locale === "zh-Hans" && key.endsWith("_one") &&
            translatedKeys.has(`${key.slice(0, -4)}_other`));
        });
        expect(missing, `missing ${locale} keys in ${namespace}`).toEqual([]);
        expect(
          [...translatedKeys].filter((key) => !enKeys.has(key)),
          `extra ${locale} keys in ${namespace}`,
        ).toEqual([]);
      }
    });
  }

  it("preserves interpolation parameters in Russian", () => {
    const enResources = readLocale("en");
    const ruResources = new Map(readLocale("ru"));
    for (const [namespace, value] of enResources) {
      const enValues = leafValues(value);
      const ruValues = leafValues(ruResources.get(namespace));
      for (const [key, english] of enValues) {
        const russian = ruValues.get(key);
        expect(russian, `missing ru text for ${namespace}:${key}`).toBeDefined();
        expect(
          placeholders(russian ?? ""),
          `changed interpolation in ${namespace}:${key}`,
        ).toEqual(placeholders(english));
      }
    }
  });
});
