import { useEffect, useState } from "react";
import { useNavigate, useParams } from "react-router";

import { CollectionForm } from "../_app.tables._index/components/table-form";
import type { CollectionConfig } from "../_app.tables._index/loader.client";

import { Card, CardContent } from "~/components/ui/card";

export default function EditCollectionRoute() {
  const { tableName } = useParams<{ tableName: string }>();
  const navigate = useNavigate();
  const [collection, setCollection] = useState<CollectionConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    const fetchCollection = async () => {
      try {
        const res = await fetch(`/api/collections/${tableName}`);
        if (!res.ok) throw new Error("Failed to fetch collection");
        const data: CollectionConfig = await res.json();
        setCollection(data);
      } catch (err) {
        setError("Failed to load collection");
        console.error(err);
      } finally {
        setLoading(false);
      }
    };

    fetchCollection();
  }, [tableName]);

  const handleSubmit = async (data: CollectionConfig) => {
    setIsSubmitting(true);
    setError(null);
    try {
      const res = await fetch(`/api/collections/${tableName}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      });

      if (res.ok) {
        navigate("/tables");
      } else {
        const error = await res.json();
        setError(error.error || "Failed to update collection");
      }
    } catch (err) {
      setError("Failed to update collection");
      console.error("Failed to update collection:", err);
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
