import { zodResolver } from "@hookform/resolvers/zod";
import { AlertCircleIcon, PlusIcon, Trash2Icon } from "lucide-react";
import { useState } from "react";
import { Controller, useForm } from "react-hook-form";

import { Alert, AlertDescription } from "~/components/ui/alert";
import { Button } from "~/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "~/components/ui/card";
import { Checkbox } from "~/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "~/components/ui/field";
import { Input } from "~/components/ui/input";
import {
  Select,
  SelectContent,
  SelectGroup,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { Separator } from "~/components/ui/separator";
import { apiDelete, apiErrorMessage, apiPatch, apiPost } from "~/lib/api";
import type { SinkConfig } from "~/lib/types";

import type { Route } from "./+types/route";
import {
  conditionOptions,
  createSinkSchema,
  emptySpecFor,
  filterToForm,
  formToFilter,
  type Condition,
  type CreateSinkForm,
  type FieldFilter,
} from "./schema";
export { clientLoader } from "./loader.client";

const EVENT_TYPES = ["INSERT", "MODIFY", "REMOVE"] as const;

export const handle = {
  breadcrumb: ({ params }: Route.LoaderArgs) => (
    <>{params.collectionName} › Sinks</>
  ),
};

const SINK_TYPE_LABEL: Record<SinkConfig["type"], string> = {
  http: "HTTP",
  eventbridge: "EventBridge",
  meilisearch: "Meilisearch",
};

type ImageType = "oldImage" | "newImage";

export default function Route({ params, loaderData }: Route.ComponentProps) {
  const { collectionName } = params;
  const { sinks } = loaderData;

  const [showCreate, setShowCreate] = useState(false);

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold">Sinks: {collectionName}</h1>
          <p className="text-sm text-muted-foreground">
            Sink type and configuration are immutable after creation. Only event
            types and filters can be edited.
          </p>
        </div>
        <Button size="sm" onClick={() => setShowCreate(true)}>
          <PlusIcon /> New Sink
        </Button>
      </div>

      {sinks.length === 0 ? (
        <Card>
          <CardContent className="pt-6 text-sm text-muted-foreground">
            No sinks configured. Create one to start delivering change events.
          </CardContent>
        </Card>
      ) : (
        <div className="space-y-4">
          {sinks.map((sink) => (
            <ExistingSinkCard
              key={sink.id}
              sink={sink}
              collectionName={collectionName}
            />
          ))}
        </div>
      )}

      <Dialog open={showCreate} onOpenChange={setShowCreate}>
        <DialogContent className="max-h-[90vh] overflow-y-auto">
          <DialogHeader>
            <DialogTitle>New Sink</DialogTitle>
            <DialogDescription>
              Create one sink at a time. The type and spec are fixed once saved.
            </DialogDescription>
          </DialogHeader>
          <CreateSinkForm
            collectionName={collectionName}
            onCreated={() => setShowCreate(false)}
          />
        </DialogContent>
      </Dialog>
    </div>
  );
}

// --- Self-managed filter builder (plain state, no RHF nesting) ---

/**
 * Edits the oldImage/newImage filter trees for a single sink. Holds its own
 * per-field condition lists and reports changes upward via `onChange`, so the
 * parent form stays free of deep array-field paths.
 */
function FilterBuilder({
  value,
  onChange,
}: {
  value: { oldImage: FieldFilter[]; newImage: FieldFilter[] };
  onChange: (next: {
    oldImage: FieldFilter[];
    newImage: FieldFilter[];
  }) => void;
}) {
  const updateImage = (image: ImageType, next: FieldFilter[]) => {
    onChange({ ...value, [image]: next });
  };

  return (
    <div className="space-y-4">
      {(["oldImage", "newImage"] as const).map((image) => (
        <ImageFilterEditor
          key={image}
          title={image === "oldImage" ? "old image" : "new image"}
          filters={value[image]}
          onChange={(next) => updateImage(image, next)}
        />
      ))}
    </div>
  );
}

function ImageFilterEditor({
  title,
  filters,
  onChange,
}: {
  title: string;
  filters: FieldFilter[];
  onChange: (next: FieldFilter[]) => void;
}) {
  const addField = () => {
    onChange([...filters, { field: "", conditions: [] }]);
  };

  const updateField = (index: number, next: FieldFilter) => {
    onChange(filters.map((f, i) => (i === index ? next : f)));
  };

  const removeField = (index: number) => {
    onChange(filters.filter((_, i) => i !== index));
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium uppercase text-muted-foreground">
          {title}
        </span>
        <Button
          type="button"
          variant="outline"
          size="sm"
          className="h-6 text-xs"
          onClick={addField}
        >
          <PlusIcon className="h-3.5 w-3.5 mr-1" /> Add field
        </Button>
      </div>

      {filters.length === 0 && (
        <p className="text-xs italic text-muted-foreground">
          No {title} filters configured
        </p>
      )}

      {filters.map((fieldFilter, index) => (
        <div
          key={index}
          className="space-y-3 rounded-2xl border border-border bg-card p-4"
        >
          <div className="flex items-center gap-2">
            <Input
              value={fieldFilter.field}
              onChange={(e) =>
                updateField(index, { ...fieldFilter, field: e.target.value })
              }
              placeholder="Field name"
              className="h-8 w-[160px] text-xs"
            />
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="ml-auto"
              onClick={() => removeField(index)}
              aria-label="Remove field"
            >
              <Trash2Icon className="h-3.5 w-3.5" />
            </Button>
          </div>
          <Separator />
          <FieldConditionEditor
            conditions={fieldFilter.conditions}
            onChange={(conditions) =>
              updateField(index, { ...fieldFilter, conditions })
            }
          />
        </div>
      ))}
    </div>
  );
}

function FieldConditionEditor({
  conditions,
  onChange,
}: {
  conditions: Condition[];
  onChange: (next: Condition[]) => void;
}) {
  const available = conditionOptions.filter(
    (opt) => !conditions.some((c) => c.type === opt.value),
  );

  const update = (index: number, next: Condition) =>
    onChange(conditions.map((c, i) => (i === index ? next : c)));

  return (
    <div className="space-y-2">
      {conditions.map((condition, index) => (
        <div key={index} className="flex items-center gap-2">
          <span className="min-w-[80px] rounded bg-secondary px-2 py-0.5 text-center text-xs font-medium">
            {conditionOptions.find((o) => o.value === condition.type)?.label ??
              condition.type}
          </span>
          {condition.type === "exists" ? (
            <Select
              value={condition.value || "true"}
              onValueChange={(v) =>
                update(index, { ...condition, value: v })
              }
            >
              <SelectTrigger className="h-7 w-[100px] text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem value="true">true</SelectItem>
                <SelectItem value="false">false</SelectItem>
              </SelectContent>
            </Select>
          ) : (
            <Input
              value={condition.value ?? ""}
              onChange={(e) =>
                update(index, { ...condition, value: e.target.value })
              }
              placeholder={`Enter ${condition.type} value`}
              className="h-7 flex-1 text-xs"
            />
          )}
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            aria-label="Remove condition"
            onClick={() => onChange(conditions.filter((_, i) => i !== index))}
          >
            <Trash2Icon className="h-3 w-3" />
          </Button>
        </div>
      ))}
      {available.length > 0 && (
        <div className="flex flex-wrap gap-1">
          {available.map((opt) => (
            <Button
              key={opt.value}
              type="button"
              variant="outline"
              size="sm"
              className="h-6 text-xs"
              onClick={() =>
                onChange([
                  ...conditions,
                  { type: opt.value, value: opt.value === "exists" ? "true" : "" },
                ])
              }
            >
              + {opt.label}
            </Button>
          ))}
        </div>
      )}
    </div>
  );
}

// --- Create one sink ---

function CreateSinkForm({
  collectionName,
  onCreated,
}: {
  collectionName: string;
  onCreated: () => void;
}) {
  const [error, setError] = useState<string | null>(null);
  const [filter, setFilter] = useState<{
    oldImage: FieldFilter[];
    newImage: FieldFilter[];
  }>({ oldImage: [], newImage: [] });

  const {
    control,
    handleSubmit,
    watch,
    setValue,
    formState: { errors, isSubmitting },
  } = useForm<CreateSinkForm>({
    resolver: zodResolver(createSinkSchema),
    defaultValues: {
      type: "http",
      spec: emptySpecFor("http"),
      eventTypes: [],
    },
  });

  const type = watch("type");

  const changeType = (t: CreateSinkForm["type"]) => {
    setValue("type", t);
    setValue("spec", emptySpecFor(t) as never);
  };

  const onSubmit = async (data: CreateSinkForm) => {
    setError(null);
    if (data.eventTypes.length === 0) {
      setError("Select at least one event type.");
      return;
    }
    const spec = (data.spec ?? {}) as Record<string, unknown>;
    const payload = {
      type: data.type,
      spec: Object.fromEntries(Object.entries(spec).filter(([, v]) => v)),
      eventTypes: [...data.eventTypes],
      filter: formToFilter(filter),
    };

    try {
      const res = await apiPost(
        `/api/collections/${collectionName}/sinks`,
        payload,
      );
      if (!res.ok) {
        setError(await apiErrorMessage(res, "Failed to create sink"));
        return;
      }
      onCreated();
      window.location.reload();
    } catch (err) {
      setError("Failed to create sink");
      console.error(err);
    }
  };

  return (
    <>
      {error && (
        <Alert variant="destructive" className="mb-4">
          <AlertCircleIcon />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
        <FieldGroup>
          <Field>
            <FieldLabel>Type</FieldLabel>
            <Controller
              name="type"
              control={control}
              render={({ field }) => (
                <Select value={field.value} onValueChange={changeType}>
                  <SelectTrigger>
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    <SelectGroup>
                      <SelectItem value="http">HTTP</SelectItem>
                      <SelectItem value="meilisearch">Meilisearch</SelectItem>
                      <SelectItem value="eventbridge">EventBridge</SelectItem>
                    </SelectGroup>
                  </SelectContent>
                </Select>
              )}
            />
          </Field>

          {type === "http" && (
            <div className="grid grid-cols-3 gap-4">
              <Field className="col-span-2">
                <FieldLabel>Endpoint *</FieldLabel>
                <Controller
                  name="spec.endpoint"
                  control={control}
                  render={({ field }) => <Input {...field} />}
                />
                <FieldError errors={[errors.spec?.endpoint]} />
              </Field>
              <Field>
                <FieldLabel>Bearer Token</FieldLabel>
                <Controller
                  name="spec.bearerToken"
                  control={control}
                  render={({ field }) => (
                    <Input {...field} type="password" autoComplete="off" />
                  )}
                />
              </Field>
            </div>
          )}

          {type === "eventbridge" && (
            <div className="space-y-4">
              <Field>
                <FieldLabel>Event Bus Name *</FieldLabel>
                <Controller
                  name="spec.eventBusName"
                  control={control}
                  render={({ field }) => (
                    <Input {...field} placeholder="my-event-bus" />
                  )}
                />
                <FieldError errors={[errors.spec?.eventBusName]} />
              </Field>
              <Field>
                <FieldLabel>Source</FieldLabel>
                <Controller
                  name="spec.source"
                  control={control}
                  render={({ field }) => (
                    <Input {...field} placeholder="conduit-mongodb" />
                  )}
                />
              </Field>
            </div>
          )}

          {type === "meilisearch" && (
            <div className="space-y-4">
              <div className="grid grid-cols-3 gap-4">
                <Field className="col-span-2">
                  <FieldLabel>Host *</FieldLabel>
                  <Controller
                    name="spec.host"
                    control={control}
                    render={({ field }) => (
                      <Input {...field} placeholder="http://meilisearch.example.com" />
                    )}
                  />
                  <FieldError errors={[errors.spec?.host]} />
                </Field>
                <Field>
                  <FieldLabel>API Key</FieldLabel>
                  <Controller
                    name="spec.apiKey"
                    control={control}
                    render={({ field }) => (
                      <Input {...field} type="password" autoComplete="off" />
                    )}
                  />
                </Field>
              </div>
              <Field>
                <FieldLabel>Index Name</FieldLabel>
                <Controller
                  name="spec.indexName"
                  control={control}
                  render={({ field }) => (
                    <Input
                      {...field}
                      placeholder="Defaults to collection name"
                    />
                  )}
                />
              </Field>
            </div>
          )}

          <EventTypesField
            value={watch("eventTypes")}
            onChange={(next) => setValue("eventTypes", next)}
            error={errors.eventTypes}
          />

          <div className="space-y-2">
            <div className="text-sm font-medium">Filter</div>
            <FilterBuilder value={filter} onChange={setFilter} />
          </div>
        </FieldGroup>

        <DialogFooter>
          <Button type="submit" disabled={isSubmitting}>
            {isSubmitting ? "Creating..." : "Create sink"}
          </Button>
        </DialogFooter>
      </form>
    </>
  );
}

function EventTypesField({
  value,
  onChange,
  error,
}: {
  value: string[];
  onChange: (next: string[]) => void;
  error?: unknown;
}) {
  return (
    <Field>
      <FieldLabel>Event Types *</FieldLabel>
      <div className="flex flex-wrap gap-4">
        {EVENT_TYPES.map((et) => (
          <label key={et} className="flex items-center gap-2 text-sm">
            <Checkbox
              checked={value.includes(et)}
              onCheckedChange={(checked) => {
                const next = checked
                  ? [...value, et]
                  : value.filter((t) => t !== et);
                onChange(next);
              }}
            />
            {et}
          </label>
        ))}
      </div>
      <FieldError errors={error ? [error] : []} />
    </Field>
  );
}

// --- Existing sink (editable only eventTypes + filter) ---

function ExistingSinkCard({
  sink,
  collectionName,
}: {
  sink: SinkConfig;
  collectionName: string;
}) {
  const [editing, setEditing] = useState(false);
  const [confirmingDelete, setConfirmingDelete] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);

  const deleteSink = async () => {
    setDeleting(true);
    setError(null);
    try {
      const res = await apiDelete(
        `/api/collections/${collectionName}/sinks/${sink.id}`,
      );
      if (!res.ok) {
        setError(await apiErrorMessage(res, "Failed to delete sink"));
        setDeleting(false);
        return;
      }
      setConfirmingDelete(false);
      window.location.reload();
    } catch (err) {
      setError("Failed to delete sink");
      setDeleting(false);
      console.error(err);
    }
  };

  const typeKey = sink.type as SinkConfig["type"];
  const label = SINK_TYPE_LABEL[typeKey] ?? sink.type;

  return (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between space-y-0">
        <CardTitle className="text-base">{label}</CardTitle>
        <div className="flex items-center gap-2">
          <Button size="sm" variant="outline" onClick={() => setEditing(!editing)}>
            {editing ? "Close" : "Edit"}
          </Button>
          <Button
            size="sm"
            variant="destructive"
            onClick={() => setConfirmingDelete(true)}
          >
            Delete
          </Button>
        </div>
      </CardHeader>

      <CardContent className="space-y-4">
        <div className="text-xs text-muted-foreground">
          <p className="font-medium text-foreground">ID</p>
          <p className="font-mono break-all">{sink.id}</p>
        </div>

        <div className="text-xs text-muted-foreground">
          <p className="font-medium text-foreground">Specification (immutable)</p>
          <pre className="mt-1 whitespace-pre-wrap break-all rounded bg-muted p-2 font-mono text-xs">
            {JSON.stringify(sink.spec, null, 2)}
          </pre>
        </div>

        <div className="text-xs text-muted-foreground">
          <span className="font-medium text-foreground">Event types: </span>
          {(sink.eventTypes ?? []).length === 0
            ? "none"
            : (sink.eventTypes ?? []).join(", ")}
        </div>

        {hasFilter(sink.filter) && (
          <div className="text-xs text-muted-foreground">
            <span className="font-medium text-foreground">Filter: </span>
            <pre className="mt-1 whitespace-pre-wrap break-all rounded bg-muted p-2 font-mono text-xs">
              {JSON.stringify(sink.filter, null, 2)}
            </pre>
          </div>
        )}

        {editing && (
          <EditSinkForm
            sink={sink}
            collectionName={collectionName}
            onSaved={() => {
              setEditing(false);
              window.location.reload();
            }}
          />
        )}
      </CardContent>

      {error && (
        <CardContent className="pt-0">
          <Alert variant="destructive">
            <AlertCircleIcon />
            <AlertDescription>{error}</AlertDescription>
          </Alert>
        </CardContent>
      )}

      <Dialog open={confirmingDelete} onOpenChange={setConfirmingDelete}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete sink</DialogTitle>
            <DialogDescription>
              Are you sure you want to delete this {label} sink ({sink.id})? This
              cannot be undone.
            </DialogDescription>
          </DialogHeader>
          <DialogFooter>
            <Button variant="outline" onClick={() => setConfirmingDelete(false)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={deleteSink}
              disabled={deleting}
            >
              {deleting ? "Deleting..." : "Delete"}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  );
}

function hasFilter(
  filter: { oldImage?: Record<string, unknown>; newImage?: Record<string, unknown> } | undefined,
): boolean {
  if (!filter) return false;
  const oldImage = filter.oldImage;
  const newImage = filter.newImage;
  return (
    (!!oldImage && Object.keys(oldImage).length > 0) ||
    (!!newImage && Object.keys(newImage).length > 0)
  );
}

function EditSinkForm({
  sink,
  collectionName,
  onSaved,
}: {
  sink: SinkConfig;
  collectionName: string;
  onSaved: () => void;
}) {
  const [error, setError] = useState<string | null>(null);
  const [eventTypes, setEventTypes] = useState<string[]>([
    ...(sink.eventTypes ?? []),
  ]);
  const [filter, setFilter] = useState<{
    oldImage: FieldFilter[];
    newImage: FieldFilter[];
  }>(() => filterToForm(sink.filter));

  const onSubmit = async () => {
    setError(null);
    if (eventTypes.length === 0) {
      setError("Select at least one event type.");
      return;
    }
    const payload = {
      eventTypes: [...eventTypes],
      filter: formToFilter(filter),
    };

    try {
      const res = await apiPatch(
        `/api/collections/${collectionName}/sinks/${sink.id}`,
        payload,
      );
      if (!res.ok) {
        setError(await apiErrorMessage(res, "Failed to update sink"));
        return;
      }
      onSaved();
    } catch (err) {
      setError("Failed to update sink");
      console.error(err);
    }
  };

  return (
    <form onSubmit={(e) => { e.preventDefault(); onSubmit(); }} className="space-y-4 border-t pt-4">
      {error && (
        <Alert variant="destructive">
          <AlertCircleIcon />
          <AlertDescription>{error}</AlertDescription>
        </Alert>
      )}

      <EventTypesField value={eventTypes} onChange={setEventTypes} />

      <div className="space-y-2">
        <div className="text-sm font-medium">Filter</div>
        <FilterBuilder value={filter} onChange={setFilter} />
      </div>

      <div className="flex justify-end">
        <Button type="submit">Save changes</Button>
      </div>
    </form>
  );
}
