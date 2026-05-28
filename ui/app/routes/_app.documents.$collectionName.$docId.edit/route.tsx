import Editor from "@monaco-editor/react";
import { ArrowLeftIcon, SaveIcon, XIcon } from "lucide-react";
import { useEffect, useState } from "react";
import { Link, useParams } from "react-router";

import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardFooter,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Field } from "~/components/ui/field";

import type { Route } from "./+types/route";
export { clientAction } from "./action.client";
export { clientLoader } from "./loader.client";

export const handle = {
  breadcrumb: ({ params }: Route.LoaderArgs) => (
    <>{params.collectionName} › Edit Document</>
  ),
};

export default function Route({ loaderData }: Route.ComponentProps) {
  const { collectionName, docId } = useParams<{
    collectionName: string;
    docId: string;
  }>();
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
        <Button variant="ghost" size="icon" asChild>
          <Link to={`/documents/${collectionName}`}>
            <ArrowLeftIcon />
          </Link>
        </Button>
        <h1 className="text-2xl font-bold">Edit Document</h1>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Document JSON</CardTitle>
          <CardDescription className="text-xs">
            Edit the document JSON below.
          </CardDescription>
        </CardHeader>

        <CardContent>
          <Field className="border rounded-2xl overflow-hidden">
            <Editor
              height="50vh"
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
          </Field>

          {error && (
            <p className="text-sm text-destructive text-center">{error}</p>
          )}
        </CardContent>

        <CardFooter className="justify-end gap-2">
          <Button type="button" variant="outline" asChild>
            <Link to={`/documents/${collectionName}`}>
              <XIcon />
              Cancel
            </Link>
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
