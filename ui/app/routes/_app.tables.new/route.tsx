import { useState } from "react";
import { useNavigate } from "react-router";

import { TableForm } from "../_app.tables._index/components/table-form";
import type { TableConfig } from "../_app.tables._index/loader.client";

import { ArrowLeftIcon } from "lucide-react";
import { Button } from "~/components/ui/button";
import { Card, CardContent } from "~/components/ui/card";

export default function NewTableRoute() {
  const navigate = useNavigate();
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (data: TableConfig) => {
    setIsSubmitting(true);
    setError(null);
    try {
      const res = await fetch("/api/tables", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      });

      if (res.ok) {
        navigate("/tables");
      } else {
        const error = await res.json();
        setError(error.error || "Failed to create table");
      }
    } catch (err) {
      setError("Failed to create table");
      console.error("Failed to create table:", err);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">New Table</h1>

      <Card>
        <CardContent className="pt-6">
          <TableForm
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
