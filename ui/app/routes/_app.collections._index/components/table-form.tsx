import { zodResolver } from "@hookform/resolvers/zod";
import { XIcon } from "lucide-react";
import { useEffect } from "react";
import { Controller, useForm } from "react-hook-form";

import { Button } from "~/components/ui/button";
import { Card, CardContent } from "~/components/ui/card";
import { Checkbox } from "~/components/ui/checkbox";
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

import type {
  CollectionConfig,
  FilterCondition,
  FilterCriteria,
} from "../loader.client";
import { FilterCriteriaEditor } from "./filter-criteria-editor";
import {
  collectionSchema,
  type CollectionForm,
  type FieldFilter,
} from "./types";

// --- Conversions (form ↔ API) ---

function formToAPICriteria(
  form: ReturnType<typeof apiToFormCriteria> | undefined,
): FilterCriteria {
  if (!form) return {};
  const criteria: FilterCriteria = {};
  for (const image of ["old_image", "new_image"] as const) {
    const filters = form[image];
    if (!filters?.length) continue;
    const filter: Record<string, FilterCondition> = {};
    for (const f of filters) {
      if (!f.field) continue;
      const cond: FilterCondition = {};
      for (const condition of f.conditions || []) {
        if (condition.type === "prefix" && condition.value) {
          cond.prefix = condition.value;
        } else if (condition.type === "suffix" && condition.value) {
          cond.suffix = condition.value;
        } else if (condition.type === "exists") {
          cond.exists = condition.value === "true";
        } else if (
          condition.type === "numeric" &&
          condition.value &&
          condition.numericOp
        ) {
          cond.numeric = [condition.numericOp, Number(condition.value) || 0];
        } else if (condition.type === "anything-but" && condition.value) {
          if (condition.value.startsWith("[")) {
            try {
              cond["anything-but"] = JSON.parse(condition.value);
            } catch {
              cond["anything-but"] = condition.value;
            }
          } else {
            cond["anything-but"] = condition.value;
          }
        }
      }
      if (Object.keys(cond).length > 0) {
        filter[f.field] = cond;
      }
    }
    if (Object.keys(filter).length > 0) {
      criteria[image] = filter;
    }
  }
  return criteria;
}

function apiToFormCriteria(criteria: FilterCriteria | undefined): {
  old_image: FieldFilter[];
  new_image: FieldFilter[];
} {
  const form: { old_image: FieldFilter[]; new_image: FieldFilter[] } = {
    old_image: [],
    new_image: [],
  };
  if (!criteria) return form;
  for (const image of ["old_image", "new_image"] as const) {
    const filter = criteria[image];
    if (!filter) continue;
    const filters: FieldFilter[] = [];
    for (const [field, cond] of Object.entries(filter)) {
      const f: FieldFilter = { field, conditions: [] };
      if (cond.prefix) {
        f.conditions.push({ type: "prefix", value: cond.prefix });
      }
      if (cond.suffix) {
        f.conditions.push({ type: "suffix", value: cond.suffix });
      }
      if (cond.exists !== undefined) {
        f.conditions.push({ type: "exists", value: String(cond.exists) });
      }
      if (cond.numeric) {
        f.conditions.push({
          type: "numeric",
          numericOp: cond.numeric[0] as ">" | "<" | ">=" | "<=" | "=",
          value: String(cond.numeric[1]),
        });
      }
      if (cond["anything-but"] !== undefined) {
        f.conditions.push({
          type: "anything-but",
          value:
            typeof cond["anything-but"] === "string"
              ? cond["anything-but"]
              : JSON.stringify(cond["anything-but"]),
        });
      }
      filters.push(f);
    }
    form[image] = filters;
  }
  return form;
}

// --- Component ---

interface CollectionFormProps {
  initialData?: CollectionConfig;
  onSubmit: (data: CollectionConfig) => Promise<void>;
  onCancel: () => void;
}

export function CollectionForm({
  initialData,
  onSubmit,
  onCancel,
}: CollectionFormProps) {
  const {
    control,
    handleSubmit,
    formState: { errors },
    watch,
    setValue,
  } = useForm<CollectionForm>({
    resolver: zodResolver(collectionSchema),
    defaultValues: {
      collection_name: initialData?.collection_name ?? "",
      primary_key: initialData?.primary_key ?? "",
      sort_key: initialData?.sort_key ?? "",
      stream_enabled: initialData?.stream_enabled ?? false,
      old_image: initialData?.old_image ?? false,
      ttl_attribute: initialData?.ttl_attribute ?? "",
      destinations: initialData
        ? initialData.destinations.map((d) => ({
            type: d.type as "http" | "eventbridge" | "meilisearch",
            endpoint: d.endpoint ?? "",
            bearer_token: d.bearer_token ?? "",
            event_types: d.event_types ?? [],
            filter_criteria: apiToFormCriteria(d.filter_criteria),
            region: d.region ?? "",
            event_bus_name: d.event_bus_name ?? "",
            source: d.source ?? "",
            index_name: d.index_name ?? "",
          }))
        : [],
      deletion_protection: initialData?.deletion_protection ?? true,
    },
  });

  const destinations = watch("destinations");
  const streamEnabled = watch("stream_enabled");

  useEffect(() => {
    if (streamEnabled && destinations.length === 0) {
      setValue("destinations", [
        {
          type: "http" as const,
          endpoint: "",
          bearer_token: "",
          event_types: [],
          filter_criteria: { old_image: [], new_image: [] },
        },
      ]);
    }
  }, [streamEnabled]);

  const submitHandler = async (data: CollectionForm) => {
    if (!data.stream_enabled) {
      data.destinations = [];
    }
    const apiData: CollectionConfig = {
      collection_name: data.collection_name,
      primary_key: data.primary_key,
      sort_key: data.sort_key,
      stream_enabled: data.stream_enabled,
      old_image: data.old_image,
      ttl_attribute: data.ttl_attribute,
      deletion_protection: data.deletion_protection,
      destinations: data.destinations.map((dest) => ({
        type: dest.type,
        endpoint: dest.endpoint,
        bearer_token: dest.bearer_token,
        event_types: dest.event_types,
        filter_criteria: formToAPICriteria(dest.filter_criteria),
        region: dest.region,
        event_bus_name: dest.event_bus_name,
        source: dest.source,
        index_name: dest.index_name,
      })),
    };

    await onSubmit(apiData);
  };

  const addDestination = () => {
    setValue("destinations", [
      ...destinations,
      {
        type: "http" as const,
        endpoint: "",
        bearer_token: "",
        event_types: [],
        filter_criteria: { old_image: [], new_image: [] },
        region: "",
        event_bus_name: "",
        source: "",
        index_name: "",
      },
    ]);
  };

  const removeDestination = (index: number) => {
    setValue(
      "destinations",
      destinations.filter((_, i) => i !== index),
    );
  };

  return (
    <form onSubmit={handleSubmit(submitHandler)} className="space-y-6">
      <FieldGroup>
        <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
          <Field>
            <FieldLabel>Collection Name *</FieldLabel>
            <Controller
              name="collection_name"
              control={control}
              render={({ field }) => (
                <Input {...field} disabled={!!initialData} />
              )}
            />
            <FieldError errors={[errors.collection_name]} />
          </Field>

          <Field>
            <FieldLabel>Primary Key</FieldLabel>
            <Controller
              name="primary_key"
              control={control}
              render={({ field }) => (
                <Input
                  {...field}
                  disabled={!!initialData?.primary_key}
                  placeholder="e.g., pk, id, userId"
                />
              )}
            />
            <FieldError errors={[errors.primary_key]} />
            {initialData?.primary_key && (
              <p className="text-xs text-muted-foreground mt-1">
                Cannot be changed after creation
              </p>
            )}
          </Field>

          <Field>
            <FieldLabel>Sort Key</FieldLabel>
            <Controller
              name="sort_key"
              control={control}
              render={({ field }) => (
                <Input
                  {...field}
                  disabled={!!initialData?.sort_key}
                  placeholder="e.g., sk, sort, createdAt"
                />
              )}
            />
            <FieldError errors={[errors.sort_key]} />
            {initialData?.sort_key && (
              <p className="text-xs text-muted-foreground mt-1">
                Cannot be changed after creation
              </p>
            )}
          </Field>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field>
            <FieldLabel>TTL Attribute</FieldLabel>
            <Controller
              name="ttl_attribute"
              control={control}
              render={({ field }) => (
                <Input {...field} disabled={!!initialData?.ttl_attribute} />
              )}
            />
            <FieldError errors={[errors.ttl_attribute]} />
          </Field>
        </div>

        <div className="flex flex-col gap-4 md:flex-row md:flex-wrap">
          <Field orientation="horizontal">
            <Controller
              name="stream_enabled"
              control={control}
              render={({ field }) => (
                <FieldLabel className="has-data-checked:bg-transparent">
                  <Checkbox
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                  Stream Enabled
                </FieldLabel>
              )}
            />
          </Field>

          <Field orientation="horizontal">
            <Controller
              name="old_image"
              control={control}
              render={({ field }) => (
                <FieldLabel className="has-data-checked:bg-transparent">
                  <Checkbox
                    checked={field.value}
                    onCheckedChange={field.onChange}
                  />
                  Old Image
                </FieldLabel>
              )}
            />
          </Field>

          <Field orientation="horizontal">
            <Controller
              name="deletion_protection"
              control={control}
              render={({ field }) => (
                <FieldLabel className="has-data-checked:bg-transparent">
                  <Checkbox
                    checked={field.value}
                    onCheckedChange={field.onChange}
                    disabled={!initialData}
                  />
                  Deletion Protection
                </FieldLabel>
              )}
            />
          </Field>
        </div>

        {streamEnabled && (
          <>
            <span className="text-xl font-medium">Destinations</span>

            <div className="space-y-6">
              {destinations.map((dest, index) => (
                <Card key={index} className="relative">
                  <CardContent className="space-y-6">
                    {destinations.length > 1 && (
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => removeDestination(index)}
                      >
                        <XIcon />
                      </Button>
                    )}

                    <Field>
                      <FieldLabel>Type</FieldLabel>
                      <Controller
                        name={`destinations.${index}.type` as const}
                        control={control}
                        render={({ field }) => (
                          <Select
                            {...field}
                            value={field.value}
                            onValueChange={field.onChange}
                            disabled={!streamEnabled}
                          >
                            <SelectTrigger>
                              <SelectValue />
                            </SelectTrigger>
                            <SelectContent>
                              <SelectGroup>
                                <SelectItem value="http">HTTP</SelectItem>
                                <SelectItem value="meilisearch">
                                  Meilisearch
                                </SelectItem>
                                <SelectItem value="eventbridge">
                                  EventBridge
                                </SelectItem>
                              </SelectGroup>
                            </SelectContent>
                          </Select>
                        )}
                      />
                    </Field>

                    {/* HTTP fields */}
                    {dest.type === "http" && (
                      <div className="grid grid-cols-3 gap-4">
                        <Field className="col-span-2">
                          <FieldLabel>Endpoint *</FieldLabel>
                          <Controller
                            name={`destinations.${index}.endpoint` as const}
                            control={control}
                            render={({ field }) => (
                              <Input {...field} disabled={!streamEnabled} />
                            )}
                          />
                          <FieldError
                            errors={[errors.destinations?.[index]?.endpoint]}
                          />
                        </Field>
                        <Field>
                          <FieldLabel>Bearer Token</FieldLabel>
                          <Controller
                            name={`destinations.${index}.bearer_token` as const}
                            control={control}
                            render={({ field }) => (
                              <Input
                                {...field}
                                type="password"
                                disabled={!streamEnabled}
                              />
                            )}
                          />
                        </Field>
                      </div>
                    )}

                    {/* EventBridge fields */}
                    {dest.type === "eventbridge" && (
                      <div className="space-y-4">
                        <div className="grid grid-cols-2 gap-4">
                          <Field>
                            <FieldLabel>Region *</FieldLabel>
                            <Controller
                              name={`destinations.${index}.region` as const}
                              control={control}
                              render={({ field }) => (
                                <Input
                                  {...field}
                                  placeholder="us-east-1"
                                  disabled={!streamEnabled}
                                />
                              )}
                            />
                          </Field>
                          <Field>
                            <FieldLabel>Event Bus Name *</FieldLabel>
                            <Controller
                              name={
                                `destinations.${index}.event_bus_name` as const
                              }
                              control={control}
                              render={({ field }) => (
                                <Input
                                  {...field}
                                  placeholder="my-event-bus"
                                  disabled={!streamEnabled}
                                />
                              )}
                            />
                          </Field>
                        </div>
                        <Field>
                          <FieldLabel>Source</FieldLabel>
                          <Controller
                            name={`destinations.${index}.source` as const}
                            control={control}
                            render={({ field }) => (
                              <Input
                                {...field}
                                placeholder="conduit-mongodb"
                                disabled={!streamEnabled}
                              />
                            )}
                          />
                        </Field>
                      </div>
                    )}

                    {/* Meilisearch fields */}
                    {dest.type === "meilisearch" && (
                      <div className="space-y-4">
                        <div className="grid grid-cols-3 gap-4">
                          <Field className="col-span-2">
                            <FieldLabel>Host *</FieldLabel>
                            <Controller
                              name={`destinations.${index}.endpoint` as const}
                              control={control}
                              render={({ field }) => (
                                <Input
                                  {...field}
                                  placeholder="http://localhost:7700"
                                  disabled={!streamEnabled}
                                />
                              )}
                            />
                            <FieldError
                              errors={[errors.destinations?.[index]?.endpoint]}
                            />
                          </Field>
                          <Field>
                            <FieldLabel>API Key</FieldLabel>
                            <Controller
                              name={
                                `destinations.${index}.bearer_token` as const
                              }
                              control={control}
                              render={({ field }) => (
                                <Input
                                  {...field}
                                  type="password"
                                  disabled={!streamEnabled}
                                />
                              )}
                            />
                          </Field>
                        </div>
                        <Field>
                          <FieldLabel>Index Name</FieldLabel>
                          <Controller
                            name={`destinations.${index}.index_name` as const}
                            control={control}
                            render={({ field }) => (
                              <Input
                                {...field}
                                placeholder="Defaults to table name"
                                disabled={!streamEnabled}
                              />
                            )}
                          />
                        </Field>
                      </div>
                    )}
                    <Field>
                      <FieldLabel>Event Types *</FieldLabel>
                      <Controller
                        name={`destinations.${index}.event_types` as const}
                        control={control}
                        render={({ field }) => (
                          <div className="flex flex-wrap gap-4">
                            {["INSERT", "MODIFY", "REMOVE"].map((et) => (
                              <label
                                key={et}
                                className="flex items-center gap-2 text-sm"
                              >
                                <Checkbox
                                  checked={field.value?.includes(et) || false}
                                  onCheckedChange={(checked) => {
                                    const next = checked
                                      ? [...(field.value || []), et]
                                      : (field.value || []).filter(
                                          (t) => t !== et,
                                        );
                                    field.onChange(next);
                                  }}
                                  disabled={!streamEnabled}
                                />
                                {et}
                              </label>
                            ))}
                          </div>
                        )}
                      />
                      <FieldError
                        errors={[errors.destinations?.[index]?.event_types]}
                      />
                    </Field>

                    {/* Filter Criteria */}
                    <div className="space-y-2">
                      <div className="text-sm font-medium">Filter Criteria</div>

                      <FilterCriteriaEditor
                        imageType="old_image"
                        destIndex={index}
                        control={control}
                      />

                      <FilterCriteriaEditor
                        imageType="new_image"
                        destIndex={index}
                        control={control}
                      />
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>

            <Button type="button" variant="outline" onClick={addDestination}>
              Add Destination
            </Button>
          </>
        )}
      </FieldGroup>

      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting
            ? initialData
              ? "Updating..."
              : "Creating..."
            : initialData
              ? "Update"
              : "Create"}
        </Button>
      </div>
    </form>
  );
}
