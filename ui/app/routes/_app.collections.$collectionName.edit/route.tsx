import { zodResolver } from "@hookform/resolvers/zod";
import { XIcon } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Controller, useFieldArray, useForm } from "react-hook-form";
import { useNavigate } from "react-router";

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
import { Separator } from "~/components/ui/separator";

import type { Route } from "./+types/route";
import {
  collectionFormSchema,
  type CollectionConfig,
  type CollectionForm,
  type Condition,
  type FieldFilter,
  type FilterCondition,
  type FilterCriteria,
} from "./schema";
export { clientAction } from "./action.client";
export { clientLoader } from "./loader.client";

export default function Route({ params, loaderData }: Route.ComponentProps) {
  const navigate = useNavigate();
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
      <h1 className="text-2xl font-bold">Edit Collection</h1>

      <Card>
        <CardContent className="pt-6">
          <UpdateCollectionForm
            initialData={collection}
            collectionName={collectionName}
            onCancel={() => navigate("/collections")}
          />
        </CardContent>
      </Card>
    </div>
  );
}

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

// --- Filter Criteria Editor ---

const conditionOptions: { value: Condition["type"]; label: string }[] = [
  { value: "prefix", label: "Prefix" },
  { value: "suffix", label: "Suffix" },
  { value: "exists", label: "Exists" },
  { value: "numeric", label: "Numeric" },
  { value: "anything-but", label: "Anything But" },
];

const numericOperators = [
  { value: ">", label: ">" },
  { value: "<", label: "<" },
  { value: ">=", label: ">=" },
  { value: "<=", label: "<=" },
  { value: "=", label: "=" },
];

interface FilterCriteriaEditorProps {
  imageType: "old_image" | "new_image";
  destIndex: number;
  control: ReturnType<typeof useForm<CollectionForm>>["control"];
}

function FilterCriteriaEditor({
  imageType,
  destIndex,
  control,
}: FilterCriteriaEditorProps) {
  const { fields, append, remove } = useFieldArray({
    control,
    name: `destinations.${destIndex}.filter_criteria.${imageType}` as const,
  });

  const addField = () => {
    append({ field: "", conditions: [] });
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
          {imageType.replace("_", " ")}
        </span>
        <Button type="button" variant="outline" size="sm" onClick={addField}>
          <PlusIcon className="h-3.5 w-3.5 mr-1" />
          Add Field
        </Button>
      </div>

      {fields.length === 0 && (
        <p className="text-xs text-muted-foreground italic">
          No filters configured
        </p>
      )}

      {fields.map((field, fieldIndex) => (
        <div
          key={field.id}
          className="border border-border rounded-2xl p-6 space-y-6 bg-card"
        >
          <div className="flex items-center gap-2">
            <Controller
              name={
                `destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.field` as const
              }
              control={control}
              render={({ field: fieldProps }) => (
                <Input
                  {...fieldProps}
                  placeholder="Enter field name..."
                  className="h-8 text-xs w-[180px]"
                />
              )}
            />
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={() => remove(fieldIndex)}
              className="ml-auto"
            >
              <Trash2Icon className="h-3.5 w-3.5" />
            </Button>
          </div>

          <Controller
            name={
              `destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.conditions` as const
            }
            control={control}
            render={({ field: conditionsProps }) => {
              const conditions = conditionsProps.value || [];
              const availableConditions = conditionOptions.filter(
                (opt) => !conditions.some((c) => c.type === opt.value),
              );

              return (
                <div className="space-y-2">
                  <Separator />

                  {conditions.map((condition, conditionIndex) => (
                    <div
                      key={conditionIndex}
                      className="flex items-center gap-2"
                    >
                      <span className="text-xs font-medium px-2 py-0.5 rounded bg-secondary text-secondary-foreground min-w-[80px] text-center">
                        {
                          conditionOptions.find(
                            (opt) => opt.value === condition.type,
                          )?.label
                        }
                      </span>

                      {condition.type === "exists" ? (
                        <Controller
                          name={
                            `destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.conditions.${conditionIndex}.value` as const
                          }
                          control={control}
                          render={({ field }) => (
                            <Select
                              value={field.value || "true"}
                              onValueChange={field.onChange}
                            >
                              <SelectTrigger className="h-7 text-xs w-[100px]">
                                <SelectValue />
                              </SelectTrigger>
                              <SelectContent>
                                <SelectItem value="true">true</SelectItem>
                                <SelectItem value="false">false</SelectItem>
                              </SelectContent>
                            </Select>
                          )}
                        />
                      ) : condition.type === "numeric" ? (
                        <>
                          <Controller
                            name={
                              `destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.conditions.${conditionIndex}.numericOp` as const
                            }
                            control={control}
                            render={({ field }) => (
                              <Select
                                value={field.value || ">"}
                                onValueChange={field.onChange}
                              >
                                <SelectTrigger className="h-7 text-xs w-[70px]">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  {numericOperators.map((op) => (
                                    <SelectItem key={op.value} value={op.value}>
                                      {op.label}
                                    </SelectItem>
                                  ))}
                                </SelectContent>
                              </Select>
                            )}
                          />
                          <Controller
                            name={
                              `destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.conditions.${conditionIndex}.value` as const
                            }
                            control={control}
                            render={({ field }) => (
                              <Input
                                {...field}
                                type="number"
                                placeholder="0"
                                className="h-7 text-xs w-[100px]"
                              />
                            )}
                          />
                        </>
                      ) : (
                        <Controller
                          name={
                            `destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.conditions.${conditionIndex}.value` as const
                          }
                          control={control}
                          render={({ field }) => (
                            <Input
                              {...field}
                              placeholder={`Enter ${condition.type} value`}
                              className="h-7 text-xs flex-1"
                            />
                          )}
                        />
                      )}

                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-sm"
                        onClick={() => {
                          const updated = conditions.filter(
                            (_, i) => i !== conditionIndex,
                          );
                          conditionsProps.onChange(updated);
                        }}
                      >
                        <Trash2Icon className="h-3 w-3" />
                      </Button>
                    </div>
                  ))}

                  {availableConditions.length > 0 && (
                    <div className="flex flex-wrap gap-1">
                      {availableConditions.map((opt) => (
                        <Button
                          key={opt.value}
                          type="button"
                          variant="outline"
                          size="sm"
                          className="h-6 text-xs"
                          onClick={() => {
                            const newCondition = {
                              type: opt.value,
                              value: opt.value === "exists" ? "true" : "",
                              numericOp:
                                opt.value === "numeric" ? ">" : undefined,
                            };
                            conditionsProps.onChange([
                              ...conditions,
                              newCondition,
                            ]);
                          }}
                        >
                          + {opt.label}
                        </Button>
                      ))}
                    </div>
                  )}
                </div>
              );
            }}
          />
        </div>
      ))}
    </div>
  );
}

import { PlusIcon, Trash2Icon } from "lucide-react";

// --- Update Collection Form Component ---

interface UpdateCollectionFormProps {
  initialData: CollectionConfig;
  collectionName: string;
  onCancel: () => void;
}

function UpdateCollectionForm({
  initialData,
  collectionName,
  onCancel,
}: UpdateCollectionFormProps) {
  const [submitError, setSubmitError] = useState<string | null>(null);

  const {
    control,
    handleSubmit,
    formState: { errors, isSubmitting },
    watch,
    setValue,
  } = useForm<CollectionForm>({
    resolver: zodResolver(collectionFormSchema),
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
  const addedInitialRef = useRef(false);

  useEffect(() => {
    if (
      streamEnabled &&
      destinations.length === 0 &&
      !addedInitialRef.current
    ) {
      setValue("destinations", [
        {
          type: "http" as const,
          endpoint: "",
          bearer_token: "",
          event_types: [],
          filter_criteria: { old_image: [], new_image: [] },
        },
      ]);
      addedInitialRef.current = true;
    }
  }, [streamEnabled, destinations.length, setValue]);

  const submitHandler = async (data: CollectionForm) => {
    setSubmitError(null);

    if (!data.stream_enabled) {
      data.destinations = [];
    }

    const payload = {
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

    try {
      const res = await fetch(`/api/collections/${collectionName}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });

      if (res.ok) {
        window.location.href = "/collections";
      } else {
        const error = await res.json();
        setSubmitError(error.error || "Failed to update collection");
      }
    } catch (err) {
      setSubmitError("Failed to update collection");
      console.error("Failed to update collection:", err);
    }
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
              render={({ field }) => <Input {...field} disabled />}
            />
            <FieldError errors={[errors.collection_name]} />
          </Field>

          <Field>
            <FieldLabel>Primary Key</FieldLabel>
            <Controller
              name="primary_key"
              control={control}
              render={({ field }) => <Input {...field} disabled />}
            />
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
              render={({ field }) => <Input {...field} disabled />}
            />
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
              render={({ field }) => <Input {...field} disabled />}
            />
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

      {submitError && <p className="text-sm text-destructive">{submitError}</p>}

      <div className="flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? "Updating..." : "Update"}
        </Button>
      </div>
    </form>
  );
}
