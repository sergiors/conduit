import { useState } from "react";
import { useNavigate } from "react-router";

import { Card, CardContent } from "~/components/ui/card";

import { CollectionForm } from "../_app.collections._index/components/table-form";
import type { CollectionConfig } from "../_app.collections._index/loader.client";
import type { Route } from "./+types/route";
export { clientLoader } from "./loader.client";

export default function Route({ params, loaderData }: Route.ComponentProps) {
  const navigate = useNavigate();
  const { collectionName } = params;
  const { collection } = loaderData;
  const [error, setError] = useState<string | null>(null);

  const handleSubmit = async (data: CollectionConfig) => {
    setError(null);
    try {
      const res = await fetch(`/api/collections/${collectionName}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      });

      if (res.ok) {
        navigate("/collections");
      } else {
        const error = await res.json();
        setError(error.error || "Failed to update collection");
      }
    } catch (err) {
      setError("Failed to update collection");
      console.error("Failed to update collection:", err);
    }
  };

  if (!collection) {
    return (
      <div className="p-4 md:p-8">
        <p className="text-destructive">Collection not found</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Edit Collection</h1>

      <Card>
        <CardContent className="pt-6">
          <CollectionForm
            initialData={collection}
            onSubmit={handleSubmit}
            onCancel={() => navigate("/tables")}
          />
          {error && (
            <p className="text-sm text-destructive text-center mt-4">{error}</p>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
