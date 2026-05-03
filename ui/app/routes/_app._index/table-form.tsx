import { useState } from "react";
import { useRevalidator } from "react-router";

import { XIcon } from "lucide-react";
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

import { zodResolver } from "@hookform/resolvers/zod";
import { Controller, useForm } from "react-hook-form";
import { z } from "zod";

import type { TableConfig } from "./loader.client";

const destinationSchema = z.object({
  type: z.enum(["http", "eventbridge"]).default("http"),
  endpoint: z.string().min(1, "Endpoint is required"),
  bearer_token: z.string().optional(),
  event_types: z.array(z.string()).min(1, "At least one event type is required"),
});

const tableSchema = z
  .object({
    table_name: z.string().min(1, "Table name is required"),
    stream_enabled: z.boolean().default(true),
    old_image: z.boolean().default(false),
    ttl_attribute: z.string().optional(),
    destinations: z
      .array(destinationSchema)
      .min(1, "At least one destination is required"),
    deletion_protection: z.boolean().default(true),
  })
  .refine(
    (data) => {
      // If stream is enabled, destinations must have valid data
      if (data.stream_enabled) {
        return data.destinations.every(
          (d) => d.endpoint && d.endpoint.length > 0 && d.event_types.length > 0
        );
      }
      return true;
    },
    {
      message: "Destinations are required when stream is enabled",
      path: ["destinations"],
    }
  );

type TableForm = z.infer<typeof tableSchema>;

interface TableFormProps {
  initialData?: TableConfig;
  onSubmit: (data: TableForm) => Promise<void>;
  onCancel: () => void;
  isSubmitting?: boolean;
}

export function TableForm({
  initialData,
  onSubmit,
  onCancel,
  isSubmitting,
}: TableFormProps) {
  const revalidator = useRevalidator();

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
          })),
          deletion_protection: initialData.deletion_protection,
        }
      : {
          table_name: "",
          stream_enabled: true,
          old_image: false,
          ttl_attribute: "",
          destinations: [
            {
              type: "http" as const,
              endpoint: "",
              event_types: [],
            },
          ],
          deletion_protection: true,
        },
  });

  const destinations = watch("destinations");
  const streamEnabled = watch("stream_enabled");

  const submitHandler = async (data: TableForm) => {
    await onSubmit(data);
  };

  const addDestination = () => {
    setValue("destinations", [
      ...destinations,
      {
        type: "http" as const,
        endpoint: "",
        event_types: [],
      },
    ]);
  };

  const removeDestination = (index: number) => {
    setValue(
      "destinations",
      destinations.filter((_, i) => i !== index),
    );
  };

  const updateDestination = (index: number, field: string, value: unknown) => {
    setValue(
      "destinations",
      destinations.map((d, i) => (i === index ? { ...d, [field]: value } : d)),
    );
  };

  return (
    <form onSubmit={handleSubmit(submitHandler)} className="space-y-6">
      <DialogHeader>
        <DialogTitle>
          {initialData ? "Edit Table" : "Create Table"}
        </DialogTitle>
      </DialogHeader>

      <FieldGroup>
        <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
          <Field>
            <FieldLabel>Table Name *</FieldLabel>
            <Controller
              name="table_name"
              control={control}
              render={({ field }) => (
                <Input {...field} placeholder="users" disabled={!!initialData} />
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
                <Input {...field} placeholder="expires_at" />
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
                  />
                  Deletion Protection
                </FieldLabel>
              )}
            />
          </Field>
        </div>

        <div className="flex items-center gap-4">
          <Separator className="flex-1" />
          <span className="text-sm font-medium">Destinations</span>
          <Separator className="flex-1" />
        </div>

        <div
          className={`space-y-4 ${!streamEnabled ? "opacity-50 pointer-events-none" : ""}`}
        >
          {destinations.map((dest, index) => (
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
                      <Input
                        {...field}
                        placeholder="https://..."
                        disabled={!streamEnabled}
                      />
                    )}
                  />
                  <FieldError errors={[errors.destinations?.[index]?.endpoint]} />
                </Field>
                <Field>
                  <FieldLabel>Bearer Token</FieldLabel>
                  <Controller
                    name={`destinations.${index}.bearer_token`}
                    control={control}
                    render={({ field }) => (
                      <Input
                        {...field}
                        placeholder="Bearer token..."
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
                        {["INSERT", "MODIFY", "REMOVE"].map((eventType) => (
                          <label
                            key={eventType}
                            className="flex items-center gap-2 text-sm"
                          >
                            <Checkbox
                              checked={field.value?.includes(eventType) || false}
                              onCheckedChange={(checked) => {
                                const newEventTypes = checked
                                  ? [...(field.value || []), eventType]
                                  : (field.value || []).filter(
                                      (t) => t !== eventType,
                                    );
                                field.onChange(newEventTypes);
                              }}
                              disabled={!streamEnabled}
                            />
                            {eventType}
                          </label>
                        ))}
                      </div>
                    )}
                  />
                  <FieldError errors={[errors.destinations?.[index]?.event_types]} />
                </Field>
              </CardContent>
            </Card>
          ))}
        </div>

        <Button
          type="button"
          variant="outline"
          onClick={addDestination}
          disabled={!streamEnabled}
        >
          Add Destination
        </Button>
      </FieldGroup>

      <DialogFooter>
        <DialogClose asChild>
          <Button type="button" variant="outline" onClick={onCancel}>
            Cancel
          </Button>
        </DialogClose>
        <Button type="submit" disabled={isSubmitting}>
          {isSubmitting ? (initialData ? "Updating..." : "Creating...") : (initialData ? "Update" : "Create")}
        </Button>
      </DialogFooter>
    </form>
  );
}
