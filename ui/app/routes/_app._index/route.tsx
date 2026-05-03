import { useState } from "react"
import { useRouteLoaderData, useRevalidator } from "react-router"
import type { Route } from "./+types/route"
import { clientLoader, type TableConfig } from "./loader.client"

import {
  Table,
  TableBody,
  TableHead,
  TableHeader,
  TableRow,
  TableCell,
} from "~/components/ui/table"
import { Card, CardContent } from "~/components/ui/card"
import { Button } from "~/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
  DialogFooter,
  DialogClose,
} from "~/components/ui/dialog"
import { Input } from "~/components/ui/input"
import { Label } from "~/components/ui/label"

import { z } from "zod"
import { useForm } from "react-hook-form"
import { zodResolver } from "@hookform/resolvers/zod"

const destinationSchema = z.object({
  type: z.string().min(1, "Type is required"),
  endpoint: z.string().optional(),
  bearer_token: z.string().optional(),
  event_types: z.array(z.string()).default(["INSERT", "MODIFY", "REMOVE"]),
})

const tableSchema = z.object({
  table_name: z.string().min(1, "Table name is required"),
  stream_enabled: z.boolean().default(true),
  old_image: z.boolean().default(false),
  ttl_attribute: z.string().optional(),
  destinations: z.array(destinationSchema).min(1, "At least one destination is required"),
  deletion_protection: z.boolean().default(true),
})

type TableForm = z.infer<typeof tableSchema>

export { clientLoader }

export function meta({}: Route.MetaArgs) {
  return [
    { title: "Tables - Relay" },
    { name: "description", content: "Relay Tables Management" },
  ]
}

function NewTableDialog() {
  const [open, setOpen] = useState(false)
  const revalidator = useRevalidator()

  const { register, handleSubmit, formState: { errors }, watch, setValue } = useForm<TableForm>({
    resolver: zodResolver(tableSchema),
    defaultValues: {
      table_name: "",
      stream_enabled: true,
      old_image: false,
      ttl_attribute: "",
      destinations: [{ type: "http", endpoint: "", event_types: ["INSERT", "MODIFY", "REMOVE"] }],
      deletion_protection: true,
    },
  })

  const destinations = watch("destinations")

  const onSubmit = async (data: TableForm) => {
    try {
      const res = await fetch("/api/tables", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      })

      if (res.ok) {
        setOpen(false)
        revalidator.revalidate()
      } else {
        const error = await res.json()
        alert(`Failed to create table: ${error.error}`)
      }
    } catch (err) {
      console.error("Failed to create table:", err)
    }
  }

  const addDestination = () => {
    setValue("destinations", [
      ...destinations,
      { type: "http", endpoint: "", event_types: ["INSERT", "MODIFY", "REMOVE"] },
    ])
  }

  const removeDestination = (index: number) => {
    setValue("destinations", destinations.filter((_, i) => i !== index))
  }

  const updateDestination = (index: number, field: string, value: unknown) => {
    setValue(
      "destinations",
      destinations.map((d, i) => (i === index ? { ...d, [field]: value } : d))
    )
  }

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogTrigger asChild>
        <Button>New Table</Button>
      </DialogTrigger>
      <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
        <DialogHeader>
          <DialogTitle>Create Table</DialogTitle>
        </DialogHeader>
        <form onSubmit={handleSubmit(onSubmit)} className="space-y-4">
          <div className="grid grid-cols-2 gap-4">
            <div className="space-y-2">
              <Label htmlFor="table_name">Table Name *</Label>
              <Input id="table_name" {...register("table_name")} placeholder="users" />
              {errors.table_name && (
                <p className="text-sm text-destructive">{errors.table_name.message}</p>
              )}
            </div>
            <div className="space-y-2">
              <Label htmlFor="ttl_attribute">TTL Attribute</Label>
              <Input
                id="ttl_attribute"
                {...register("ttl_attribute")}
                placeholder="expires_at"
              />
              {errors.ttl_attribute && (
                <p className="text-sm text-destructive">{errors.ttl_attribute.message}</p>
              )}
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="flex items-center space-x-2">
              <input
                type="checkbox"
                id="stream_enabled"
                checked={watch("stream_enabled")}
                onChange={(e) => setValue("stream_enabled", e.target.checked)}
                className="h-4 w-4"
              />
              <Label htmlFor="stream_enabled">Stream Enabled</Label>
            </div>
            <div className="flex items-center space-x-2">
              <input
                type="checkbox"
                id="old_image"
                checked={watch("old_image")}
                onChange={(e) => setValue("old_image", e.target.checked)}
                className="h-4 w-4"
              />
              <Label htmlFor="old_image">Old Image</Label>
            </div>
          </div>

          <div className="flex items-center space-x-2">
            <input
              type="checkbox"
              id="deletion_protection"
              checked={watch("deletion_protection")}
              onChange={(e) => setValue("deletion_protection", e.target.checked)}
              className="h-4 w-4"
            />
            <Label htmlFor="deletion_protection">Deletion Protection</Label>
          </div>

          <div className="border rounded-md p-4 space-y-4">
            <Label>Destinations</Label>
            {destinations.map((dest, index) => (
              <div key={index} className="grid grid-cols-2 gap-4 border-b pb-4 relative">
                <div className="space-y-2">
                  <Label>Type</Label>
                  <Input
                    value={dest.type}
                    onChange={(e) => updateDestination(index, "type", e.target.value)}
                    placeholder="http"
                  />
                </div>
                <div className="space-y-2">
                  <Label>Endpoint</Label>
                  <Input
                    value={dest.endpoint || ""}
                    onChange={(e) => updateDestination(index, "endpoint", e.target.value)}
                    placeholder="https://..."
                  />
                </div>
                <div className="col-span-2 space-y-2">
                  <Label>Bearer Token</Label>
                  <Input
                    value={dest.bearer_token || ""}
                    onChange={(e) => updateDestination(index, "bearer_token", e.target.value)}
                    placeholder="Bearer token..."
                    type="password"
                  />
                </div>
                <div className="col-span-2 space-y-2">
                  <Label>Event Types (comma-separated)</Label>
                  <Input
                    value={dest.event_types?.join(", ") || ""}
                    onChange={(e) => {
                      const eventTypes = e.target.value
                        .split(",")
                        .map((s) => s.trim().toUpperCase())
                        .filter(Boolean)
                      updateDestination(index, "event_types", eventTypes)
                    }}
                    placeholder="INSERT, MODIFY, REMOVE"
                  />
                </div>
                {destinations.length > 1 && (
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    onClick={() => removeDestination(index)}
                    className="absolute top-0 right-0"
                  >
                    Remove
                  </Button>
                )}
              </div>
            ))}
            <Button type="button" variant="outline" onClick={addDestination}>
              Add Destination
            </Button>
          </div>

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
  )
}

export default function Route() {
  const { tables } = useRouteLoaderData<typeof clientLoader>("routes/_app._index")

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
                    <TableCell className="font-medium">{table.table_name}</TableCell>
                    <TableCell>
                      <span
                        className={`text-sm ${
                          table.stream_enabled ? "text-green-600" : "text-muted-foreground"
                        }`}
                      >
                        {table.stream_enabled ? "Yes" : "No"}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span
                        className={`text-sm ${
                          table.old_image ? "text-green-600" : "text-muted-foreground"
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
                          table.deletion_protection ? "text-green-600" : "text-red-600"
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
  )
}

function DestinationsCell({ destinations }: { destinations: TableConfig["destinations"] }) {
  return (
    <Dialog>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          {destinations.length} {destinations.length === 1 ? "destination" : "destinations"}
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
                  <span className="text-muted-foreground text-sm">→ {d.endpoint}</span>
                )}
              </div>
              {d.bearer_token && (
                <div className="text-sm text-muted-foreground">
                  <span className="font-medium">Bearer Token:</span> {d.bearer_token.substring(0, 20)}...
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
  )
}
