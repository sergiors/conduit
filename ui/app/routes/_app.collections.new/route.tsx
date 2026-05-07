import { useState } from "react";
import { useNavigate } from "react-router";

import { CollectionForm } from "../_app.collections._index/components/table-form";
import type { CollectionConfig } from "../_app.collections._index/loader.client";

import { Card, CardContent } from "~/components/ui/card";

export default function NewCollectionRoute() {
  const navigate = useNavigate();
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (data: CollectionConfig) => {
    await new Promise((r) => setTimeout(r, 2000));

    setError(null);
    try {
      const res = await fetch("/api/collections", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      });

      if (res.ok) {
        navigate("/collections");
      } else {
        const error = await res.json();
        setError(error.error || "Failed to create collection");
      }
    } catch (err) {
      setError("Failed to create collection");
      console.error("Failed to create collection:", err);
    }
  };

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">New Collection</h1>

      <Card>
        <CardContent className="pt-6">
          <CollectionForm
            onSubmit={handleSubmit}
            onCancel={() => navigate("/collections")}
          />
          {error && (
            <p className="text-sm text-destructive text-center mt-4">{error}</p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
