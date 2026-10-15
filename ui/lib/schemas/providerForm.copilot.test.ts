import { describe, expect, it } from "vitest";
import { DefaultNetworkConfig } from "@/lib/constants/config";
import { modelProviderKeySchema } from "@/lib/types/schemas";
import { ProviderFormSchema } from "./providerForm";

// The provider form validates through ProviderFormSchema, and zodResolver hands
// react-hook-form the parsed result. Zod strips undeclared keys, so a credential the schema
// does not know about never reaches the API. That failure is silent: the form saves, reports
// success, and the key arrives with no sign-in at all.
describe("ProviderFormSchema github-copilot credentials", () => {
	const literal = (value: string) => ({ value, ref: "" });
	const envRef = (ref: string) => ({ value: "", ref, type: "env" as const });

	const form = (keyOverrides: Record<string, unknown>) => ({
		selectedProvider: "github-copilot",
		isDirty: true,
		networkConfig: DefaultNetworkConfig,
		keys: [{ id: "k1", name: "copilot", value: "", models: ["*"], weight: 1, ...keyOverrides }],
	});

	const parse = (keyOverrides: Record<string, unknown>) => ProviderFormSchema.safeParse(form(keyOverrides));

	it("accepts a token with no sign-in", () => {
		expect(parse({ value: "github_pat_abc" }).success).toBe(true);
	});

	it("rejects a key with neither a token nor a sign-in client ID", () => {
		expect(parse({}).success, "a key with no credential at all must not save").toBe(false);
		// The nested schema permits an empty block so token auth stays valid, so the outer check
		// is the only thing standing between this and a credential-less key.
		expect(parse({ github_copilot_key_config: {} }).success).toBe(false);
	});

	describe("sign in with GitHub", () => {
		it("accepts a client ID alone, since the token is stored by the gateway", () => {
			const parsed = parse({ github_copilot_key_config: { oauth_client_id: literal("Iv23liAbC.12") } });

			expect(parsed.success, parsed.success ? "" : JSON.stringify(parsed.error.issues)).toBe(true);
			if (!parsed.success) return;
			expect(parsed.data.keys[0].github_copilot_key_config?.oauth_client_id?.value, "the client ID was stripped").toBe("Iv23liAbC.12");
		});

		it("accepts a client ID from an env reference", () => {
			expect(parse({ github_copilot_key_config: { oauth_client_id: envRef("env.COPILOT_CLIENT_ID") } }).success).toBe(true);
		});

		it("accepts the masked client ID a key read returns", () => {
			expect(parse({ github_copilot_key_config: { oauth_client_id: literal("Iv23" + "*".repeat(24) + "C.12") } }).success).toBe(true);
		});

		it("rejects something that is not a client ID", () => {
			expect(parse({ github_copilot_key_config: { oauth_client_id: literal("Iv23 li/../x") } }).success).toBe(false);
		});
	});

	// Both tabs' fields stay in the form so switching tabs keeps them; only the selected tab may
	// block a save, or a field on the hidden tab would refuse it with an error the user cannot see.
	describe("selected auth tab", () => {
		const keyParse = (value: string, config: Record<string, unknown>) =>
			modelProviderKeySchema.safeParse({
				id: "k1",
				name: "copilot",
				value: literal(value),
				models: ["*"],
				weight: 1,
				github_copilot_key_config: config,
			});

		it("ignores a malformed client ID while the token tab is selected", () => {
			expect(keyParse("github_pat_x", { oauth_client_id: literal("not a client id!"), _auth_type: "token" }).success).toBe(true);
		});

		it("requires the token on the token tab, even with a client ID set", () => {
			expect(keyParse("", { oauth_client_id: literal("Iv23liAbC"), _auth_type: "token" }).success).toBe(false);
		});

		it("requires a client ID on the sign in tab, even with a token set", () => {
			expect(keyParse("github_pat_x", { _auth_type: "sign_in" }).success).toBe(false);
			expect(keyParse("github_pat_x", { oauth_client_id: literal("Iv23liAbC"), _auth_type: "sign_in" }).success).toBe(true);
		});
	});
});