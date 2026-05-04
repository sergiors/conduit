import { zodResolver } from "@hookform/resolvers/zod";
import { PlusIcon, XIcon } from "lucide-react";
import { useEffect } from "react";
import { Controller, useForm } from "react-hook-form";
import { z } from "zod";

import { Button } from "~/components/ui/button";
import {
  Card,
  CardAction,
  CardContent,
  CardHeader,
} from "~/components/ui/card";
import { Checkbox } from "~/components/ui/checkbox";
import {
  DialogClose,
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

import type { FilterCondition, FilterCriteria, TableConfig } from "./loader.client";

// --- Schemas ---

const fieldFilterSchema = z.object({
  field: z.string().min(1, "Field name is required"),
  prefix: z.string().optional(),
  suffix: z.string().optional(),
  exists: z.string().optional(), // "" | "true" | "false"
  numericOp: z.enum([">", "<", ">=", "<=", "="]).optional(),
  numericVal: z.string().optional(),
  anythingBut: z.string().optional(),
});

const filterCriteriaSchema = z.object({
  old_image: z.array(fieldFilterSchema).optional(),
  new_image: z.array(fieldFilterSchema).optional(),
});

const destinationSchema = z.object({
  type: z.enum(["http", "eventbridge"]),
  endpoint: z.string().min(1, "Endpoint is required"),
  bearer_token: z.string().optional(),
  event_types: z
    .array(z.string())
    .min(1, "At least one event type is required"),
  filter_criteria: filterCriteriaSchema.optional(),
});

const tableSchema = z
  .object({
    table_name: z.string().min(1, "Table name is required"),
    stream_enabled: z.boolean(),
    old_image: z.boolean(),
    ttl_attribute: z.string().optional(),
    destinations: z.array(destinationSchema),
    deletion_protection: z.boolean(),
  })
  .refine((data) => !data.stream_enabled || data.destinations.length > 0, {
    message: "At least one destination is required",
    path: ["destinations"],
  });

type TableForm = z.infer<typeof tableSchema>;
type FieldFilter = z.infer<typeof fieldFilterSchema>;

// --- Conversions (form ↔ API) ---

function formToAPICriteria(
  form: z.infer<typeof filterCriteriaSchema> | undefined,
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
      if (f.prefix) cond.prefix = f.prefix;
      if (f.suffix) cond.suffix = f.suffix;
      if (f.exists === "true") cond.exists = true;
      else if (f.exists === "false") cond.exists = false;
      if (f.numericOp && f.numericVal) {
        cond.numeric = [f.numericOp, Number(f.numericVal) || 0];
      }
      if (f.anythingBut) {
        if (f.anythingBut.startsWith("[")) {
          try { cond["anything-but"] = JSON.parse(f.anythingBut); } catch { cond["anything-but"] = f.anythingBut; }
        } else {
          cond["anything-but"] = f.anythingBut;
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

function apiToFormCriteria(
  criteria: FilterCriteria | undefined,
): z.infer<typeof filterCriteriaSchema> {
  const form: z.infer<typeof filterCriteriaSchema> = {};
  if (!criteria) return form;
  for (const image of ["old_image", "new_image"] as const) {
    const filter = criteria[image];
    if (!filter) continue;
    const filters: FieldFilter[] = [];
    for (const [field, cond] of Object.entries(filter)) {
      const f: FieldFilter = { field };
      if (cond.prefix) f.prefix = cond.prefix;
      if (cond.suffix) f.suffix = cond.suffix;
      if (cond.exists !== undefined) f.exists = String(cond.exists);
      if (cond.numeric) {
        f.numericOp = String(cond.numeric[0]) as FieldFilter["numericOp"];
        f.numericVal = String(cond.numeric[1]);
      }
      if (cond["anything-but"] !== undefined) {
        f.anythingBut = typeof cond["anything-but"] === "string"
          ? cond["anything-but"]
          : JSON.stringify(cond["anything-but"]);
      }
      filters.push(f);
    }
    form[image] = filters;
  }
  return form;
}

const emptyFieldFilter = (): FieldFilter => ({
  field: "",
  prefix: "",
  suffix: "",
  exists: "",
  numericOp: undefined,
  numericVal: "",
  anythingBut: "",
});

// --- Component ---

interface TableFormProps {
  initialData?: TableConfig;
  onSubmit: (data: any) => Promise<void>;
  onCancel: () => void;
  isSubmitting?: boolean;
}

export function TableForm({
  initialData,
  onSubmit,
  onCancel,
  isSubmitting,
}: TableFormProps) {
  const {
    control,
    handleSubmit,
    formState: { errors },
    watch,
    setValue,
  } = useForm<TableForm>({
    resolver: zodResolver(tableSchema),
    defaultValues: initialData
      ? {
          table_name: initialData.table_name,
          stream_enabled: initialData.stream_enabled,
          old_image: initialData.old_image,
          ttl_attribute: initialData.ttl_attribute || "",
          destinations: initialData.destinations.map((d) => ({
            type: d.type as "http" | "eventbridge",
            endpoint: d.endpoint || "",
            bearer_token: d.bearer_token || "",
            event_types: d.event_types || [],
            filter_criteria: apiToFormCriteria(d.filter_criteria),
          })),
          deletion_protection: initialData.deletion_protection,
        }
      : {
          table_name: "",
          stream_enabled: false,
          old_image: false,
          ttl_attribute: "",
          destinations: [],
          deletion_protection: true,
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

  const submitHandler = async (data: TableForm) => {
    if (!data.stream_enabled) {
      data.destinations = [];
    }
    const apiData = {
      ...data,
      destinations: data.destinations.map((dest) => ({
        ...dest,
        filter_criteria: formToAPICriteria(dest.filter_criteria),
      })),
    };
    await onSubmit(apiData as any);
  };

  const addDestination = () => {
    setValue("destinations", [
      ...destinations,
      {
        type: "http" as const,
        endpoint: "",
        event_types: [],
        filter_criteria: { old_image: [], new_image: [] },
      },
    ]);
  };

  const removeDestination = (index: number) => {
    setValue(
      "destinations",
      destinations.filter((_, i) => i !== index),
    );
  };

  const addFieldFilter = (destIndex: number, image: "old_image" | "new_image") => {
    const updated = [...destinations];
    const fc = updated[destIndex].filter_criteria || { old_image: [], new_image: [] };
    updated[destIndex] = {
      ...updated[destIndex],
      filter_criteria: {
        ...fc,
        [image]: [...(fc[image] || []), emptyFieldFilter()],
      },
    };
    setValue("destinations", updated);
  };

  const removeFieldFilter = (destIndex: number, image: "old_image" | "new_image", fieldIndex: number) => {
    const updated = [...destinations];
    const fc = updated[destIndex].filter_criteria || { old_image: [], new_image: [] };
    updated[destIndex] = {
      ...updated[destIndex],
      filter_criteria: {
        ...fc,
        [image]: (fc[image] || []).filter((_, i) => i !== fieldIndex),
      },
    };
    setValue("destinations", updated);
  };

  const renderFieldFilterRow = (
    destIndex: number,
    image: "old_image" | "new_image",
    fieldIndex: number,
    filter: FieldFilter,
  ) => (
    <div key={fieldIndex} className="space-y-1.5 p-2 border rounded-md">
      <div className="flex items-center gap-2">
        <Controller
          name={`destinations.${destIndex}.filter_criteria.${image}.${fieldIndex}.field`}
          control={control}
          render={({ field }) => (
            <Input {...field} placeholder="field name" className="w-28 h-8 text-xs" />
          )}
        />
        <div className="flex-1" />
        <Button
          type="button"
          variant="ghost"
          size="icon-sm"
          onClick={() => removeFieldFilter(destIndex, image, fieldIndex)}
        >
          <XIcon />
        </Button>
      </div>

      <div className="grid grid-cols-2 gap-1.5">
        <div className="flex items-center gap-1">
          <span className="text-[10px] text-muted-foreground w-12">prefix</span>
          <Controller
            name={`destinations.${destIndex}.filter_criteria.${image}.${fieldIndex}.prefix`}
            control={control}
            render={({ field }) => (
              <Input {...field} placeholder="prefix" className="h-7 text-xs flex-1" />
            )}
          />
        </div>

        <div className="flex items-center gap-1">
          <span className="text-[10px] text-muted-foreground w-12">suffix</span>
          <Controller
            name={`destinations.${destIndex}.filter_criteria.${image}.${fieldIndex}.suffix`}
            control={control}
            render={({ field }) => (
              <Input {...field} placeholder="suffix" className="h-7 text-xs flex-1" />
            )}
          />
        </div>

        <div className="flex items-center gap-1">
          <span className="text-[10px] text-muted-foreground w-12">exists</span>
          <Controller
            name={`destinations.${destIndex}.filter_criteria.${image}.${fieldIndex}.exists`}
            control={control}
            render={({ field }) => (
              <Select value={field.value || ""} onValueChange={field.onChange}>
                <SelectTrigger className="h-7 text-xs flex-1">
                  <SelectValue placeholder="—" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value="true">true</SelectItem>
                  <SelectItem value="false">false</SelectItem>
                </SelectContent>
              </Select>
            )}
          />
        </div>

        <div className="flex items-center gap-1">
          <span className="text-[10px] text-muted-foreground w-12">a-but</span>
          <Controller
            name={`destinations.${destIndex}.filter_criteria.${image}.${fieldIndex}.anythingBut`}
            control={control}
            render={({ field }) => (
              <Input {...field} placeholder="val or [a,b]" className="h-7 text-xs flex-1" />
            )}
          />
        </div>

        <div className="flex items-center gap-1 col-span-2">
          <span className="text-[10px] text-muted-foreground w-12">numeric</span>
          <Controller
            name={`destinations.${destIndex}.filter_criteria.${image}.${fieldIndex}.numericOp`}
            control={control}
            render={({ field }) => (
              <Select
                value={field.value || ""}
                onValueChange={(v) => {
                  field.onChange(v || undefined);
                  if (!v) {
                    const updated = [...destinations];
                    const f = updated[destIndex].filter_criteria?.[image]?.[fieldIndex];
                    if (f) f.numericVal = "";
                    setValue("destinations", updated);
                  }
                }}
              >
                <SelectTrigger className="h-7 text-xs w-14">
                  <SelectValue placeholder="op" />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem value=">">&gt;</SelectItem>
                  <SelectItem value="<">&lt;</SelectItem>
                  <SelectItem value=">=">&gt;=</SelectItem>
                  <SelectItem value="<=">&lt;=</SelectItem>
                  <SelectItem value="=">=</SelectItem>
                </SelectContent>
              </Select>
            )}
          />
          <Controller
            name={`destinations.${destIndex}.filter_criteria.${image}.${fieldIndex}.numericVal`}
            control={control}
            render={({ field }) => (
              <Input {...field} placeholder="value" className="h-7 text-xs w-20" />
            )}
          />
        </div>
      </div>
    </div>
  );

  return (
    <form onSubmit={handleSubmit(submitHandler)} className="space-y-6">
      <DialogHeader>
        <DialogTitle>{initialData ? "Edit Table" : "Create Table"}</DialogTitle>
      </DialogHeader>

      <FieldGroup>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field>
            <FieldLabel>Table Name *</FieldLabel>
            <Controller
              name="table_name"
              control={control}
              render={({ field }) => (
                <Input {...field} disabled={!!initialData} />
              )}
            />
            <FieldError errors={[errors.table_name]} />
          </Field>

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
            <div className="flex items-center gap-4">
              <Separator className="flex-1" />
              <span className="text-sm font-medium">Destinations</span>
              <Separator className="flex-1" />
            </div>

            <div className="space-y-4">
              {destinations.map((dest, index) => {
                const fc = dest.filter_criteria || { old_image: [], new_image: [] };
                const oldFilters = fc.old_image || [];
                const newFilters = fc.new_image || [];

                return (
                  <Card key={index} className="relative">
                    {destinations.length > 1 && (
                      <CardHeader>
                        <CardAction>
                          <Button
                            type="button"
                            variant="ghost"
                            size="icon-sm"
                            onClick={() => removeDestination(index)}
                          >
                            <XIcon />
                          </Button>
                        </CardAction>
                      </CardHeader>
                    )}
                    <CardContent className="space-y-4">
                      <Field>
                        <FieldLabel>Type</FieldLabel>
                        <Controller
                          name={`destinations.${index}.type`}
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
                                  <SelectItem value="eventbridge" disabled>
                                    EventBridge
                                  </SelectItem>
                                </SelectGroup>
                              </SelectContent>
                            </Select>
                          )}
                        />
                      </Field>
                      <Field>
                        <FieldLabel>Endpoint *</FieldLabel>
                        <Controller
                          name={`destinations.${index}.endpoint`}
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
                          name={`destinations.${index}.bearer_token`}
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
                      <Field>
                        <FieldLabel>Event Types *</FieldLabel>
                        <Controller
                          name={`destinations.${index}.event_types`}
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
                                        : (field.value || []).filter((t) => t !== et);
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

                      <Separator />

                      {/* Filter Criteria */}
                      <div className="space-y-3">
                        <span className="text-sm font-medium">Filter Criteria</span>

                        {/* old_image */}
                        <div className="space-y-1.5">
                          <div className="flex items-center justify-between">
                            <span className="text-xs text-muted-foreground">old_image</span>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              onClick={() => addFieldFilter(index, "old_image")}
                            >
                              <PlusIcon className="h-3 w-3 mr-1" />
                              Add Field
                            </Button>
                          </div>
                          {oldFilters.map((f, fi) =>
                            renderFieldFilterRow(index, "old_image", fi, f),
                          )}
                        </div>

                        {/* new_image */}
                        <div className="space-y-1.5">
                          <div className="flex items-center justify-between">
                            <span className="text-xs text-muted-foreground">new_image</span>
                            <Button
                              type="button"
                              variant="ghost"
                              size="sm"
                              onClick={() => addFieldFilter(index, "new_image")}
                            >
                              <PlusIcon className="h-3 w-3 mr-1" />
                              Add Field
                            </Button>
                          </div>
                          {newFilters.map((f, fi) =>
                            renderFieldFilterRow(index, "new_image", fi, f),
                          )}
                        </div>
                      </div>
                    </CardContent>
                  </Card>
                );
              })}
            </div>

            <Button type="button" variant="outline" onClick={addDestination}>
              Add Destination
            </Button>
          </>
        )}
      </FieldGroup>

      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline" onClick={onCancel}>
            Cancel
          </Button>
        </DialogClose>

        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting
            ? initialData
              ? "Updating..."
              : "Creating..."
            : initialData
              ? "Update"
              : "Create"}
        </Button>
      </DialogFooter>
    </form>
  );
}
