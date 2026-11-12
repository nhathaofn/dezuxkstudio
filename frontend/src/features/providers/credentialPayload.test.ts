import { describe, expect, it } from "vitest";
import type { BuiltinProvider } from "../../contracts/generated";
import { buildCredentialPayload } from "./credentialPayload";

const provider: Pick<BuiltinProvider, "fields" | "credential_groups"> = {
  credential_groups: [["api_key"], ["access_key", "secret_key"]],
  fields: [
    { key: "base_url", kind: "url", secret: false, required: false, placeholder: "" },
    ...["api_key", "access_key", "secret_key"].map((key) => ({
      key,
      kind: "text" as const,
      secret: true,
      required: true,
      placeholder: "",
    })),
  ],
};

describe("buildCredentialPayload", () => {
  it("splits values and secrets and excludes secrets from the other credential group", () => {
    const inputs = {
      base_url: "https://example.test",
      api_key: "api",
      access_key: "access",
      secret_key: "secret",
    };
    expect(buildCredentialPayload(provider, "  account  ", inputs, 0)).toEqual({
      name: "account",
      values: { base_url: inputs.base_url },
      secrets: { api_key: "api" },
      clear_secrets: [],
    });
    expect(buildCredentialPayload(provider, "", inputs, 1).secrets).toEqual({
      access_key: "access",
      secret_key: "secret",
    });
  });

  it("omits empty edit secrets so the server keeps stored values", () => {
    expect(buildCredentialPayload(provider, "", { api_key: "" }, 0)).toEqual({
      name: "",
      values: { base_url: "" },
      secrets: {},
      clear_secrets: [],
    });
  });

  it("sends explicit clears only for known secret fields in the selected group", () => {
    expect(
      buildCredentialPayload(provider, "", { api_key: "replacement" }, 0, [
        "api_key",
        "access_key",
        "base_url",
        "unknown",
      ])
    ).toEqual({
      name: "",
      values: { base_url: "" },
      secrets: {},
      clear_secrets: ["api_key"],
    });
  });

  it("preserves JSON file content as a secret and ignores arbitrary inputs", () => {
    const fileProvider: typeof provider = {
      credential_groups: [],
      fields: [
        { key: "credentials_file", kind: "file", secret: true, required: true, placeholder: "" },
      ],
    };
    const json = '{"private_key":"example\\nkey"}';
    expect(
      buildCredentialPayload(fileProvider, "", { credentials_file: json, unknown: "ignored" }, 0)
        .secrets
    ).toEqual({ credentials_file: json });
  });
});
