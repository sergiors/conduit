import { useRouteLoaderData } from "react-router"
import type { Route } from "./+types/route"
import { clientLoader, type TableConfig } from "./loader.client"

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
      <div className="border rounded-lg">
        <table className="w-full">
          <thead className="bg-muted">
            <tr>
              <th className="p-3 text-left font-medium">Name</th>
              <th className="p-3 text-left font-medium">Stream</th>
              <th className="p-3 text-left font-medium">Old Image</th>
              <th className="p-3 text-left font-medium">TTL</th>
              <th className="p-3 text-left font-medium">Destinations</th>
              <th className="p-3 text-left font-medium">Protection</th>
            </tr>
          </thead>
          <tbody>
            {tables.map((table) => (
              <tr key={table._id || table.table_name} className="border-t">
                <td className="p-3 font-medium">{table.table_name}</td>
                <td className="p-3">
                  <span
                    className={`text-sm ${
                      table.stream_enabled
                        ? "text-green-600"
                        : "text-muted-foreground"
                    }`}
                  >
                    {table.stream_enabled ? "Yes" : "No"}
                  </span>
                </td>
                <td className="p-3">
                  <span
                    className={`text-sm ${
                      table.old_image ? "text-green-600" : "text-muted-foreground"
                    }`}
                  >
                    {table.old_image ? "Yes" : "No"}
                  </span>
                </td>
                <td className="p-3 text-muted-foreground">
                  {table.ttl_attribute || "-"}
                </td>
                <td className="p-3">
                  {table.destinations.map((d, i) => (
                    <div key={i} className="text-sm">
                      {d.type}
                      {d.endpoint && ` → ${d.endpoint}`}
                      <span className="text-muted-foreground ml-1">
                        ({d.event_types?.join(", ") || "ALL"})
                      </span>
                    </div>
                  ))}
                </td>
                <td className="p-3">
                  <span
                    className={`text-sm ${
                      table.deletion_protection
                        ? "text-green-600"
                        : "text-red-600"
                    }`}
                  >
                    {table.deletion_protection ? "Enabled" : "Disabled"}
                  </span>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}
