import { useState } from "react";
import { useNavigate, useParams } from "react-router";

import { Button } from "~/components/ui/button";
import { Card, CardContent } from "~/components/ui/card";
import { Field, FieldGroup, FieldLabel } from "~/components/ui/field";

import Editor from "@monaco-editor/react";
import { ArrowLeftIcon, SaveIcon, XIcon } from "lucide-react";
import { Link } from "react-router";

export default function NewDocumentRoute() {
  const { collectionName } = useParams<{ collectionName: string }>();
  const navigate = useNavigate();
  const [jsonValue, setJsonValue] = useState("{}");
  const [error, setError] = useState<string | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);

  const handleSubmit = async () => {
    setIsSubmitting(true);
    setError(null);
    try {
      const doc = JSON.parse(jsonValue);
      const res = await fetch(`/api/collections/${collectionName}/documents`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(doc),
      });

      if (res.ok) {
        navigate(`/documents/${collectionName}`);
      } else {
        const error = await res.json();
        setError(error.error || "Failed to create document");
      }
    } catch (err: unknown) {
      if (err instanceof Error) {
        setError(`Invalid JSON: ${err.message}`);
      } else {
        setError("Failed to create document");
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
        <h1 className="text-2xl font-bold">New Document</h1>
      </div>

      <Card>
        <CardContent className="p-4 space-y-4">
          <FieldGroup>
            <Field>
              <FieldLabel>Document JSON</FieldLabel>
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

          <div className="flex justify-end gap-2">
            <Button
              type="button"
              variant="outline"
              onClick={() =>
                navigate(`/collections/${collectionName}/documents`)
              }
            >
              <XIcon className="h-4 w-4 mr-2" />
              Cancel
            </Button>
            <Button onClick={handleSubmit} disabled={isSubmitting}>
              <SaveIcon className="h-4 w-4 mr-2" />
              {isSubmitting ? "Creating..." : "Save"}
            </Button>
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
