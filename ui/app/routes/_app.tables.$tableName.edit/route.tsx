import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";

import { TableForm } from "../_app.tables._index/components/table-form";
import type { TableConfig } from "../_app.tables._index/loader.client";

import { Card, CardContent } from "~/components/ui/card";

export default function EditTableRoute() {
  const { tableName } = useParams<{ tableName: string }>();
  const navigate = useNavigate();
  const [table, setTable] = useState<TableConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const fetchTable = async () => {
      try {
        const res = await fetch(`/api/tables/${tableName}`);
        if (!res.ok) throw new Error("Failed to fetch table");
        const data: TableConfig = await res.json();
        setTable(data);
      } catch (err) {
        setError("Failed to load table");
        console.error(err);
      } finally {
        setLoading(false);
      }
    };

    fetchTable();
  }, [tableName]);

  const handleSubmit = async (data: TableConfig) => {
    setIsSubmitting(true);
    setError(null);
    try {
      const res = await fetch(`/api/tables/${tableName}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      });

      if (res.ok) {
        navigate("/tables");
      } else {
        const error = await res.json();
        setError(error.error || "Failed to update table");
      }
    } catch (err) {
      setError("Failed to update table");
      console.error("Failed to update table:", err);
    } finally {
      setIsSubmitting(false);
    }
  };

  if (loading) {
    return (
      <div className="p-4 md:p-8">
        <p className="text-muted-foreground">Loading...</p>
      </div>
    );
  }

  if (!table) {
    return (
      <div className="p-4 md:p-8">
        <p className="text-destructive">Table not found</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Edit Table</h1>

      <Card>
        <CardContent className="pt-6">
          <TableForm
            initialData={table}
            onSubmit={handleSubmit}
            onCancel={() => navigate("/tables")}
            isSubmitting={isSubmitting}
          />
          {error && (
            <p className="text-sm text-destructive text-center mt-4">{error}</p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
