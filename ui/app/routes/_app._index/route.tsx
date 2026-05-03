import { useRouteLoaderData } from "react-router"
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

export { clientLoader }

export function meta({}: Route.MetaArgs) {
  return [
    { title: "Tables - Relay" },
    { name: "description", content: "Relay Tables Management" },
  ]
}

export default function Route() {
  const { tables } = useRouteLoaderData<typeof clientLoader>("routes/_app._index")

  if (!tables || tables.length === 0) {
    return (
      <div className="p-8">
        <h1 className="text-2xl font-bold mb-4">Tables</h1>
        <p className="text-muted-foreground">No tables configured.</p>
      </div>
    )
  }

  return (
    <div className="p-8">
      <h1 className="text-2xl font-bold mb-4">Tables</h1>
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
                {table.destinations.map((d, i) => (
                  <div key={i} className="text-sm">
                    {d.type}
                    {d.endpoint && ` → ${d.endpoint}`}
                    <span className="text-muted-foreground ml-1">
                      ({d.event_types?.join(", ") || "ALL"})
                    </span>
                  </div>
                ))}
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
    </div>
  )
}
