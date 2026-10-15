import { Alert, AlertDescription, AlertTitle } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
	getErrorMessage,
	useGetGithubCopilotSignInQuery,
	usePollGithubCopilotSignInMutation,
	useSignOutGithubCopilotSignInMutation,
	useStartGithubCopilotSignInMutation,
} from "@/lib/store";
import { GithubCopilotDeviceCode } from "@/lib/types/config";
import { CheckCircle2, Copy, ExternalLink, TriangleAlert } from "lucide-react";
import { useEffect, useState } from "react";
import { toast } from "sonner";

interface Props {
	provider: string;
	keyId: string;
	// The form's client ID: a literal or env reference. A masked value maps back to the key's
	// saved client ID on the server.
	clientId: string;
}

// A device flow in progress, pinned to the client ID it was started with.
type PendingSignIn = GithubCopilotDeviceCode & { clientId: string };

const EXPIRED_MESSAGE = "The code expired before sign-in finished. Start again.";

// GithubCopilotSignIn runs "Sign in with GitHub" for one Copilot key. The gateway calls GitHub
// (its device-flow endpoints send no CORS headers); this shows the code and polls.
export function GithubCopilotSignIn({ provider, keyId, clientId }: Props) {
	const { data: status } = useGetGithubCopilotSignInQuery({ provider, keyId }, { skip: !keyId });
	const [startSignIn, { isLoading: isStarting }] = useStartGithubCopilotSignInMutation();
	const [pollSignIn] = usePollGithubCopilotSignInMutation();
	const [signOut, { isLoading: isSigningOut }] = useSignOutGithubCopilotSignInMutation();
	const [pending, setPending] = useState<PendingSignIn | null>(null);
	const [error, setError] = useState<string | null>(null);
	const [justSignedIn, setJustSignedIn] = useState(false);

	// Polls at the interval GitHub asks for until sign-in finishes, the code expires, the
	// operator cancels, or the form closes.
	useEffect(() => {
		if (!pending) return;
		let cancelled = false;
		let interval = Math.max(pending.interval, 1);
		const deadline = Date.now() + pending.expires_in * 1000;
		let timer: ReturnType<typeof setTimeout>;
		const finish = (message: string | null) => {
			setPending(null);
			setError(message);
		};
		const tick = async () => {
			if (Date.now() > deadline) {
				finish(EXPIRED_MESSAGE);
				return;
			}
			try {
				const result = await pollSignIn({ provider, keyId, clientId: pending.clientId, deviceCode: pending.device_code }).unwrap();
				if (cancelled) return;
				if (result.status === "complete") {
					setJustSignedIn(true);
					finish(null);
					return;
				}
				if (result.status === "expired") {
					finish(EXPIRED_MESSAGE);
					return;
				}
				if (result.status === "denied") {
					finish("Sign-in was cancelled on GitHub.");
					return;
				}
				if (result.interval) interval = result.interval;
			} catch (err) {
				if (!cancelled) finish(getErrorMessage(err));
				return;
			}
			if (!cancelled) timer = setTimeout(tick, interval * 1000);
		};
		timer = setTimeout(tick, interval * 1000);
		return () => {
			cancelled = true;
			clearTimeout(timer);
		};
	}, [pending, provider, keyId, pollSignIn]);

	const handleStart = async () => {
		setError(null);
		setJustSignedIn(false);
		try {
			const code = await startSignIn({ provider, keyId, clientId }).unwrap();
			setPending({ ...code, clientId });
		} catch (err) {
			setError(getErrorMessage(err));
		}
	};

	const handleSignOut = async () => {
		setError(null);
		setJustSignedIn(false);
		try {
			await signOut({ provider, keyId }).unwrap();
		} catch (err) {
			setError(getErrorMessage(err));
		}
	};

	const copyCode = async (code: string) => {
		try {
			await navigator.clipboard.writeText(code);
			toast.success("Code copied");
		} catch {
			toast.error("Could not copy the code; select it and copy it instead");
		}
	};

	if (pending) {
		const verificationURI = pending.verification_uri.startsWith("https://") ? pending.verification_uri : "https://github.com/login/device";
		return (
			<div className="space-y-3 rounded-sm border p-3" data-testid="copilot-sign-in-pending">
				<p className="text-sm">Open GitHub and enter this code. This form updates by itself once you approve.</p>
				<div className="flex items-center gap-2">
					<code data-testid="copilot-sign-in-user-code" className="bg-muted rounded-sm px-3 py-1.5 font-mono text-lg tracking-widest">
						{pending.user_code}
					</code>
					<Button
						type="button"
						variant="outline"
						size="sm"
						onClick={() => copyCode(pending.user_code)}
						dataTestId="copilot-sign-in-copy-code"
					>
						<Copy className="size-4" />
						Copy
					</Button>
				</div>
				<div className="flex items-center gap-2">
					<Button type="button" size="sm" asChild>
						<a href={verificationURI} target="_blank" rel="noopener noreferrer" data-testid="copilot-sign-in-verification-link">
							Open {verificationURI.replace("https://", "")}
							<ExternalLink className="size-4" />
						</a>
					</Button>
					<Button type="button" variant="ghost" size="sm" onClick={() => setPending(null)} dataTestId="copilot-sign-in-cancel">
						Cancel
					</Button>
				</div>
			</div>
		);
	}

	return (
		<div className="space-y-3">
			{status?.signed_in && (
				<Alert data-testid="copilot-sign-in-status">
					<CheckCircle2 className="text-green-600" />
					<AlertTitle>{status.login ? `Signed in as @${status.login}` : "Signed in to GitHub"}</AlertTitle>
					<AlertDescription>
						<p>Bifrost refreshes the GitHub token automatically, so this stays signed in.</p>
						{justSignedIn && <p>Save the key to start using this sign-in.</p>}
					</AlertDescription>
				</Alert>
			)}
			{status?.status === "needs_reauth" && (
				<Alert variant="warning" data-testid="copilot-sign-in-needs-reauth">
					<TriangleAlert />
					<AlertTitle>Sign in again</AlertTitle>
					<AlertDescription>
						<p>{status.status_reason || "The GitHub sign-in is no longer valid."}</p>
					</AlertDescription>
				</Alert>
			)}
			{error && (
				<Alert variant="destructive" data-testid="copilot-sign-in-error">
					<TriangleAlert />
					<AlertDescription>
						<p>{error}</p>
					</AlertDescription>
				</Alert>
			)}
			<div className="flex items-center gap-2">
				<Button
					type="button"
					variant={status?.signed_in ? "outline" : "default"}
					size="sm"
					onClick={handleStart}
					disabled={!clientId.trim() || isStarting}
					isLoading={isStarting}
					dataTestId="copilot-sign-in-button"
				>
					{status?.signed_in ? "Sign in again" : "Sign in with GitHub"}
				</Button>
				{status?.signed_in && (
					<Button
						type="button"
						variant="ghost"
						size="sm"
						onClick={handleSignOut}
						disabled={isSigningOut}
						isLoading={isSigningOut}
						dataTestId="copilot-sign-out-button"
					>
						Sign out
					</Button>
				)}
			</div>
		</div>
	);
}