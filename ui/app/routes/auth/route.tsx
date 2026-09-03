import { useState } from "react";
import { useNavigate } from "react-router";
import { EyeIcon, EyeOffIcon, KeyRoundIcon, Loader2Icon } from "lucide-react";

import { Alert, AlertDescription, AlertTitle } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Field, FieldLabel } from "~/components/ui/field";
import { Input } from "~/components/ui/input";

import { verifyToken } from "~/lib/api";
import { useAuth } from "~/components/auth-provider";

import type { Route } from "./+types/route";
export { clientLoader } from "./action.client";

export default function Route({}: Route.ComponentProps) {
  const { state } = useAuth();

  if (state === "checking") return <CheckingScreen />;
  if (state === "unreachable") return <UnreachableScreen />;
  return <TokenScreen />;
}

function CheckingScreen() {
  return (
    <Shell>
      <div className="flex flex-col items-center gap-3 text-muted-foreground">
        <Loader2Icon className="size-6 animate-spin" />
        <p className="text-sm">Checking API token…</p>
      </div>
    </Shell>
  );
}

function UnreachableScreen() {
  const [retrying, setRetrying] = useState(false);
  const navigate = useNavigate();
  const { token, clearToken, setAuthState } = useAuth();

  const retry = async () => {
    setRetrying(true);
    if (!token) {
      setAuthState("none");
      return;
    }
    const result = await verifyToken(token);
    if (result.status === "ok") {
      setAuthState("authenticated");
      navigate("/collections", { replace: true });
    } else if (result.status === "invalid") {
      clearToken();
      setAuthState("none");
    } else {
      setRetrying(false);
    }
  };

  const disconnect = () => {
    clearToken();
    setAuthState("none");
    navigate("/auth", { replace: true });
  };

  return (
    <Shell>
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle>Unable to connect</CardTitle>
          <CardDescription>
            The API server could not be reached. Your token has been kept.
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          <Button onClick={retry} disabled={retrying}>
            {retrying ? "Retrying…" : "Retry"}
          </Button>
          <Button variant="outline" onClick={disconnect}>
            Disconnect
          </Button>
        </CardContent>
      </Card>
    </Shell>
  );
}

function TokenScreen() {
  const [token, setTokenValue] = useState("");
  const [show, setShow] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const navigate = useNavigate();
  const { setToken, setAuthState } = useAuth();

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    const value = token.trim();
    if (!value) return;

    setSubmitting(true);
    setError(null);

    const result = await verifyToken(value);
    if (result.status === "ok") {
      setToken(value);
      setAuthState("authenticated");
      navigate("/collections", { replace: true });
    } else if (result.status === "invalid") {
      setError("Invalid API token. Please check and try again.");
      setSubmitting(false);
    } else {
      setError("Unable to connect to the API server. Please try again later.");
      setSubmitting(false);
    }
  };

  return (
    <Shell>
      <Card className="w-full max-w-md">
        <CardHeader>
          <CardTitle className="flex items-center gap-2">
            <KeyRoundIcon className="size-5" />
            API Token
          </CardTitle>
          <CardDescription>
            Enter your API token to connect to the API.
          </CardDescription>
        </CardHeader>
        <CardContent>
          <form onSubmit={submit} className="space-y-4">
            <Field>
              <FieldLabel htmlFor="api-token">Token</FieldLabel>
              <div className="relative">
                <Input
                  id="api-token"
                  type={show ? "text" : "password"}
                  value={token}
                  onChange={(e) => setTokenValue(e.target.value)}
                  placeholder="Paste your API token"
                  autoComplete="off"
                  autoFocus
                  aria-invalid={!!error}
                />
                <button
                  type="button"
                  onClick={() => setShow((s) => !s)}
                  className="absolute inset-y-0 right-3 flex items-center text-muted-foreground hover:text-foreground"
                  aria-label={show ? "Hide token" : "Show token"}
                >
                  {show ? (
                    <EyeOffIcon className="size-4" />
                  ) : (
                    <EyeIcon className="size-4" />
                  )}
                </button>
              </div>
            </Field>

            {error && (
              <Alert variant="destructive">
                <AlertTitle>Connection failed</AlertTitle>
                <AlertDescription>{error}</AlertDescription>
              </Alert>
            )}

            <Button
              type="submit"
              className="w-full"
              disabled={submitting || !token.trim()}
            >
              {submitting ? "Connecting…" : "Connect"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </Shell>
  );
}

function Shell({ children }: { children: React.ReactNode }) {
  return (
    <main className="flex min-h-screen items-center justify-center p-4">
      {children}
    </main>
  );
}
