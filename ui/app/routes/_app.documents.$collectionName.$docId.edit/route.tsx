import Editor from "@monaco-editor/react";
import { ArrowLeftIcon, SaveIcon, XIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { Button } from "~/components/ui/button";
import { Card, CardContent, CardFooter } from "~/components/ui/card";
import { Field, FieldGroup, FieldLabel } from "~/components/ui/field";

import type { Route } from "./+types/route";
export { clientAction } from "./action.client";
export { clientLoader } from "./loader.client";

export default function Route({ loaderData }: Route.ComponentProps) {
  const { collectionName, docId } = useParams<{
    collectionName: string;
    docId: string;
  }>();
  const navigate = useNavigate();
  const [jsonValue, setJsonValue] = useState("{}");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  useEffect(() => {
    if (loaderData?.document) {
      const { _id, ...rest } = loaderData.document;
      setJsonValue(JSON.stringify(rest, null, 2));
    }
  }, [loaderData]);

  const handleSubmit = async () => {
    setIsSubmitting(true);
    setError(null);

    try {
      const updateData = JSON.parse(jsonValue);

      const res = await fetch(
        `/api/collections/${collectionName}/documents/${docId}`,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(updateData),
        },
      );

      if (res.ok) {
        // navigate(`/collections/${collectionName}/documents`);
      } else {
        const error = await res.json();
        setError(error.error || "Failed to update document");
      }
    } catch (err: unknown) {
      if (err instanceof Error) {
        setError(`Invalid JSON: ${err.message}`);
      } else {
        setError("Failed to update document");
      }
      console.error(err);
    } finally {
      setIsSubmitting(false);
    }
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-4">
        <Link to={`/documents/${collectionName}`}>
          <Button variant="ghost" size="icon">
            <ArrowLeftIcon className="h-4 w-4" />
          </Button>
        </Link>
        <h1 className="text-2xl font-bold">Edit Document</h1>
      </div>

      <Card>
        <CardContent>
          <FieldGroup>
            <Field>
              <FieldLabel>Document JSON</FieldLabel>
              <p className="text-xs text-muted-foreground">
                Edit the document JSON below.
              </p>
              <div className="border rounded-md overflow-hidden">
                <Editor
                  height="500px"
                  language="json"
                  value={jsonValue}
                  onChange={(value) => setJsonValue(value || "{}")}
                  theme="vs-dark"
                  options={{
                    minimap: { enabled: false },
                    fontSize: 13,
                    lineNumbers: "on",
                    automaticLayout: true,
                    tabSize: 2,
                    wordWrap: "on",
                  }}
                />
              </div>
            </Field>
          </FieldGroup>

          {error && (
            <p className="text-sm text-destructive text-center">{error}</p>
          )}
        </CardContent>

        <CardFooter className="gap-2 justify-end">
          <Button
            type="button"
            variant="outline"
            onClick={() => navigate(`/collections/${collectionName}/documents`)}
          >
            <XIcon />
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={isSubmitting}>
            <SaveIcon />
            {isSubmitting ? "Updating..." : "Save"}
          </Button>
        </CardFooter>
      </Card>
    </div>
  );
}
