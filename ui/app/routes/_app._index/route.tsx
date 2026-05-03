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
import { Card, CardContent, CardHeader, CardTitle } from "~/components/ui/card"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "~/components/ui/dialog"
import { Button } from "~/components/ui/button"

export { clientLoader }

export function meta({}: Route.MetaArgs) {
  return [
    { title: "Tables - Relay" },
    { name: "description", content: "Relay Tables Management" },
  ]
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
      <Card>
        <CardHeader>
          <CardTitle>Tables</CardTitle>
        </CardHeader>
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
