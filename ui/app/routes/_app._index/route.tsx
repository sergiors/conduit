import { useState } from "react";
import { useRevalidator, useRouteLoaderData } from "react-router";
import type { Route } from "./+types/route";
import { clientLoader, type TableConfig } from "./loader.client";

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
  Dialog,
  DialogClose,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "~/components/ui/dialog";
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "~/components/ui/field";
import { Input } from "~/components/ui/input";
import { Separator } from "~/components/ui/separator";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table";

import { zodResolver } from "@hookform/resolvers/zod";
import { Controller, useForm } from "react-hook-form";
import { z } from "zod";

const destinationSchema = z.object({
  type: z.string().min(1, "Type is required"),
  endpoint: z.string().optional(),
  bearer_token: z.string().optional(),
  event_types: z.array(z.string()).default(["INSERT", "MODIFY", "REMOVE"]),
});

const tableSchema = z.object({
  table_name: z.string().min(1, "Table name is required"),
  stream_enabled: z.boolean().default(true),
  old_image: z.boolean().default(false),
  ttl_attribute: z.string().optional(),
  destinations: z
    .array(destinationSchema)
    .min(1, "At least one destination is required"),
  deletion_protection: z.boolean().default(true),
});

type TableForm = z.infer<typeof tableSchema>;

export { clientLoader };

export function meta({}: Route.MetaArgs) {
  return [
    { title: "Tables - Relay" },
    { name: "description", content: "Relay Tables Management" },
  ];
}

function NewTableDialog() {
  const [open, setOpen] = useState(false);
  const revalidator = useRevalidator();

  const {
    control,
    handleSubmit,
    formState: { errors },
    watch,
    setValue,
  } = useForm<TableForm>({
    resolver: zodResolver(tableSchema),
    defaultValues: {
      table_name: "",
      stream_enabled: true,
      old_image: false,
      ttl_attribute: "",
      destinations: [
        {
          type: "http",
          endpoint: "",
          event_types: ["INSERT", "MODIFY", "REMOVE"],
        },
      ],
      deletion_protection: true,
    },
  });

  const destinations = watch("destinations");

  const onSubmit = async (data: TableForm) => {
    try {
      const res = await fetch("/api/tables", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      });

      if (res.ok) {
        setOpen(false);
        revalidator.revalidate();
      } else {
        const error = await res.json();
        alert(`Failed to create table: ${error.error}`);
      }
    } catch (err) {
      console.error("Failed to create table:", err);
    }
  };

  const addDestination = () => {
    setValue("destinations", [
      ...destinations,
      {
        type: "http",
        endpoint: "",
        event_types: ["INSERT", "MODIFY", "REMOVE"],
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
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button>New Table</Button>
      </DialogTrigger>
      <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Create Table</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit(onSubmit)} className="space-y-6">
          <FieldGroup>
            <div className="grid grid-cols-2 gap-4">
              <Field>
                <FieldLabel>Table Name *</FieldLabel>
                <Controller
                  name="table_name"
                  control={control}
                  render={({ field }) => (
                    <Input {...field} placeholder="users" />
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

            <div className="flex gap-6">
              <Field orientation="horizontal">
                <Controller
                  name="stream_enabled"
                  control={control}
                  render={({ field }) => (
                    <FieldLabel>
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
                    <FieldLabel>
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
                    <FieldLabel>
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

            <div className="space-y-4">
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
                    <div className="grid grid-cols-2 gap-4">
                      <Field>
                        <FieldLabel>
                          Type
                          <Input
                            value={dest.type}
                            onChange={(e) =>
                              updateDestination(index, "type", e.target.value)
                            }
                            placeholder="http"
                          />
                        </FieldLabel>
                      </Field>
                      <Field>
                        <FieldLabel>
                          Endpoint
                          <Input
                            value={dest.endpoint || ""}
                            onChange={(e) =>
                              updateDestination(index, "endpoint", e.target.value)
                            }
                            placeholder="https://..."
                          />
                        </FieldLabel>
                      </Field>
                    </div>
                    <Field>
                      <FieldLabel>
                        Bearer Token
                        <Input
                          value={dest.bearer_token || ""}
                          onChange={(e) =>
                            updateDestination(
                              index,
                              "bearer_token",
                              e.target.value,
                            )
                          }
                          placeholder="Bearer token..."
                          type="password"
                        />
                      </FieldLabel>
                    </Field>
                    <div className="flex gap-4">
                      {["INSERT", "MODIFY", "REMOVE"].map((eventType) => (
                        <label
                          key={eventType}
                          className="flex items-center gap-2 text-sm"
                        >
                          <Checkbox
                            checked={
                              dest.event_types?.includes(eventType) || false
                            }
                            onCheckedChange={(checked) => {
                              const newEventTypes = checked
                                ? [...(dest.event_types || []), eventType]
                                : (dest.event_types || []).filter(
                                    (t) => t !== eventType,
                                  );
                              updateDestination(
                                index,
                                "event_types",
                                newEventTypes,
                              );
                            }}
                          />
                          {eventType}
                        </label>
                      ))}
                    </div>
                  </CardContent>
                </Card>
              ))}
            </div>

            <Button type="button" variant="outline" onClick={addDestination}>
              Add Destination
            </Button>
          </FieldGroup>

          <DialogFooter>
            <DialogClose asChild>
              <Button type="button" variant="outline">
                Cancel
              </Button>
            </DialogClose>
            <Button type="submit">Create</Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export default function Route() {
  const { tables } =
    useRouteLoaderData<typeof clientLoader>("routes/_app._index");

  return (
    <div className="p-8">
      <div className="flex items-center justify-between mb-4">
        <h1 className="text-2xl font-bold">Tables</h1>
        <NewTableDialog />
      </div>
      <Card>
        <CardContent>
          {tables.length === 0 ? (
            <p className="text-muted-foreground">No tables configured.</p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Stream</TableHead>
                  <TableHead>Old Image</TableHead>
                  <TableHead>TTL</TableHead>
                  <TableHead>Destinations</TableHead>
                  <TableHead>Protection</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {tables.map((table) => (
                  <TableRow key={table._id || table.table_name}>
                    <TableCell className="font-medium">
                      {table.table_name}
                    </TableCell>
                    <TableCell>
                      <span
                        className={`text-sm ${
                          table.stream_enabled
                            ? "text-green-600"
                            : "text-muted-foreground"
                        }`}
                      >
                        {table.stream_enabled ? "Yes" : "No"}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span
                        className={`text-sm ${
                          table.old_image
                            ? "text-green-600"
                            : "text-muted-foreground"
                        }`}
                      >
                        {table.old_image ? "Yes" : "No"}
                      </span>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {table.ttl_attribute || "-"}
                    </TableCell>
                    <TableCell>
                      <DestinationsCell destinations={table.destinations} />
                    </TableCell>
                    <TableCell>
                      <span
                        className={`text-sm ${
                          table.deletion_protection
                            ? "text-green-600"
                            : "text-red-600"
                        }`}
                      >
                        {table.deletion_protection ? "Enabled" : "Disabled"}
                      </span>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}
        </CardContent>
      </Card>
    </div>
  );
}

function DestinationsCell({
  destinations,
}: {
  destinations: TableConfig["destinations"];
}) {
  return (
    <Dialog>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          {destinations.length}{" "}
          {destinations.length === 1 ? "destination" : "destinations"}
        </Button>
      </DialogTrigger>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>Destinations</DialogTitle>
        </DialogHeader>
        <div className="space-y-4">
          {destinations.map((d, i) => (
            <div key={i} className="border rounded-lg p-4 space-y-2">
              <div className="flex items-center gap-2">
                <span className="font-medium">{d.type}</span>
                {d.endpoint && (
                  <span className="text-muted-foreground text-sm">
                    → {d.endpoint}
                  </span>
                )}
              </div>
              {d.bearer_token && (
                <div className="text-sm text-muted-foreground">
                  <span className="font-medium">Bearer Token:</span>{" "}
                  {d.bearer_token.substring(0, 20)}...
                </div>
              )}
              <div className="text-sm">
                <span className="font-medium">Event Types:</span>{" "}
                <span className="text-muted-foreground">
                  {d.event_types?.join(", ") || "ALL"}
                </span>
              </div>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  );
}
