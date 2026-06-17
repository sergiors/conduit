import { useState, type FormEvent } from "react";
import { Link, useFetcher } from "react-router";

import { AlertCircleIcon } from "lucide-react";
import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Checkbox } from "~/components/ui/checkbox";
import { Field, FieldLabel } from "~/components/ui/field";
import { Input } from "~/components/ui/input";
import { Switch } from "~/components/ui/switch";

import type { CollectionConfig } from "~/routes/_app/loader.client";
import type { Route } from "./+types/route";

export { clientLoader } from "./loader.client";

export const handle = {
  breadcrumb: ({ params }: Route.LoaderArgs) => (
    <>{params.collectionName} › Settings</>
  ),
};

type ActionData = { error?: string; ok?: boolean };

function fetcherError(data: unknown): string | null {
  return (data as ActionData | undefined)?.error ?? null;
}

export default function SettingsRoute({
  params,
  loaderData,
}: Route.ComponentProps) {
  const { collectionName } = params;
  const { collection } = loaderData;

  if (!collection) {
    return (
      <div className="p-4 md:p-8">
        <p className="text-destructive">Collection not found</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Settings</h1>
        <Button variant="outline" size="sm" asChild>
          <Link to="/collections">Back</Link>
        </Button>
      </div>

      <GeneralCard collection={collection} />
      <ProtectionCard collection={collection} />
      <TTLCard collection={collection} />
      <StreamCard collection={collection} collectionName={collectionName} />
    </div>
  );
}

function GeneralCard({ collection }: { collection: CollectionConfig }) {
  return (
    <Card>
      <CardHeader>
        <CardTitle>General</CardTitle>
        <CardDescription>
          Collection identity and key schema. These are set at creation and
          cannot be changed.
        </CardDescription>
      </CardHeader>
      <CardContent className="grid grid-cols-1 gap-4 md:grid-cols-3">
        <Field>
          <FieldLabel htmlFor="collection_name">Collection Name</FieldLabel>
          <Input
            id="collection_name"
            value={collection.collection_name}
            disabled
            readOnly
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="partition_key">Partition Key</FieldLabel>
          <Input
            id="partition_key"
            value={collection.partition_key ?? ""}
            disabled
            readOnly
          />
        </Field>
        <Field>
          <FieldLabel htmlFor="sort_key">Sort Key</FieldLabel>
          <Input
            id="sort_key"
            value={collection.sort_key ?? ""}
            disabled
            readOnly
          />
        </Field>
      </CardContent>
    </Card>
  );
}

function ProtectionCard({ collection }: { collection: CollectionConfig }) {
  const fetcher = useFetcher();
  const error = fetcherError(fetcher.data);
  const enabled = collection.deletion_protection;
  const busy = fetcher.state !== "idle";

  const toggle = () => {
    fetcher.submit(
      {},
      {
        method: enabled ? "delete" : "put",
        action: `/collections/${collection.collection_name}/protection`,
      },
    );
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Deletion protection</CardTitle>
        <CardDescription>
          Protects the collection from being deleted unintentionally. When this
          setting is on, the collection cannot be deleted.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {error && (
          <Alert variant="destructive">
            <AlertCircleIcon />
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Status</span>
            <span
              className={`font-medium ${enabled ? "text-amber-600" : "text-green-600"}`}
            >
              {enabled ? "On" : "Off"}
            </span>
          </div>
          <Button
            variant={enabled ? "outline" : "default"}
            size="sm"
            disabled={busy}
            onClick={toggle}
          >
            {busy ? "Saving..." : enabled ? "Turn off" : "Turn on"}
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}

function TTLCard({ collection }: { collection: CollectionConfig }) {
  const fetcher = useFetcher();
  const error = fetcherError(fetcher.data);
  const enabled = !!collection.ttl_attribute;
  const attribute = collection.ttl_attribute ?? "";
  const busy = fetcher.state !== "idle";
  const [attr, setAttr] = useState(attribute);

  const turnOn = (e: FormEvent) => {
    e.preventDefault();
    if (!attr) return;
    fetcher.submit(
      { attribute: attr },
      {
        method: "put",
        action: `/collections/${collection.collection_name}/ttl`,
        encType: "application/json",
      },
    );
  };

  const turnOff = () => {
    fetcher.submit(
      {},
      {
        method: "delete",
        action: `/collections/${collection.collection_name}/ttl`,
      },
    );
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Time to Live (TTL)</CardTitle>
        <CardDescription>
          Automatically delete expired items based on a TTL attribute.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {error && (
          <Alert variant="destructive">
            <AlertCircleIcon />
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Status</span>
            <span
              className={`font-medium ${enabled ? "text-green-600" : "text-muted-foreground"}`}
            >
              {enabled ? "On" : "Off"}
            </span>
            {enabled && (
              <span className="text-xs text-muted-foreground">
                {" · attribute "}
                <code className="rounded bg-muted px-1">{attribute}</code>
              </span>
            )}
          </div>
          {enabled ? (
            <Button variant="outline" size="sm" disabled={busy} onClick={turnOff}>
              {busy ? "Saving..." : "Turn off"}
            </Button>
          ) : (
            <form onSubmit={turnOn} className="flex items-center gap-2">
              <Input
                value={attr}
                onChange={(e) => setAttr(e.target.value)}
                placeholder="e.g. expiresAt"
                className="w-48"
              />
              <Button type="submit" size="sm" disabled={busy || !attr}>
                Turn on
              </Button>
            </form>
          )}
        </div>
        {enabled && (
          <p className="text-xs text-muted-foreground">
            TTL attribute is immutable. Turn off to change it.
          </p>
        )}
      </CardContent>
    </Card>
  );
}

function StreamCard({
  collection,
  collectionName,
}: {
  collection: CollectionConfig;
  collectionName: string;
}) {
  const fetcher = useFetcher();
  const error = fetcherError(fetcher.data);
  const enabled = collection.stream_enabled;
  const oldImage = collection.old_image;
  const busy = fetcher.state !== "idle";
  const [includeOld, setIncludeOld] = useState(oldImage);

  const submitStream = (oldImageValue: boolean) => {
    fetcher.submit(
      { old_image: oldImageValue },
      {
        method: "put",
        action: `/collections/${collection.collection_name}/stream`,
        encType: "application/json",
      },
    );
  };

  const turnOn = (e: FormEvent) => {
    e.preventDefault();
    submitStream(includeOld);
  };

  const turnOff = () => {
    fetcher.submit(
      {},
      {
        method: "delete",
        action: `/collections/${collection.collection_name}/stream`,
      },
    );
  };

  return (
    <Card>
      <CardHeader>
        <CardTitle>Streaming (CDC)</CardTitle>
        <CardDescription>
          Enable change data capture for this collection. Changes are streamed
          to the configured sinks.
        </CardDescription>
      </CardHeader>
      <CardContent className="space-y-4">
        {error && (
          <Alert variant="destructive">
            <AlertCircleIcon />
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        )}
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-center gap-2">
            <span className="text-sm text-muted-foreground">Status</span>
            <span
              className={`font-medium ${enabled ? "text-green-600" : "text-muted-foreground"}`}
            >
              {enabled ? "On" : "Off"}
            </span>
            {enabled && (
              <span className="text-xs text-muted-foreground">
                {" · old image "}
                {oldImage ? "Yes" : "No"}
              </span>
            )}
          </div>
          {enabled ? (
            <div className="flex items-center gap-3">
              <label className="flex items-center gap-2 text-sm">
                <Switch
                  checked={oldImage}
                  onCheckedChange={(v) => submitStream(v === true)}
                  disabled={busy}
                  aria-label="Include old image"
                />
                Old image
              </label>
              <Button variant="outline" size="sm" disabled={busy} onClick={turnOff}>
                {busy ? "Saving..." : "Turn off"}
              </Button>
            </div>
          ) : (
            <form onSubmit={turnOn} className="flex items-center gap-3">
              <label className="flex items-center gap-2 text-sm">
                <Checkbox
                  checked={includeOld}
                  onCheckedChange={(v) => setIncludeOld(v === true)}
                />
                Include old image
              </label>
              <Button type="submit" size="sm" disabled={busy}>
                Turn on
              </Button>
            </form>
          )}
        </div>
        {enabled && (
          <p className="text-sm text-muted-foreground">
            Configure sinks in the{" "}
            <Link
              to={`/collections/${collectionName}/sinks`}
              className="underline"
            >
              Sinks page
            </Link>
            .
          </p>
        )}
      </CardContent>
    </Card>
  );
}