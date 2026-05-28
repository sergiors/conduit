import Editor from "@monaco-editor/react";
import { ArrowLeftIcon, SaveIcon, XIcon } from "lucide-react";
import { useState } from "react";
import { Link, useNavigate, useParams } from "react-router";

import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardFooter,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import { Field } from "~/components/ui/field";

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
        <Button variant="ghost" size="icon" asChild>
          <Link to={`/documents/${collectionName}`}>
            <ArrowLeftIcon />
          </Link>
        </Button>
        <h1 className="text-2xl font-bold">New Document</h1>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Document JSON</CardTitle>
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
            {isSubmitting ? "Creating..." : "Save"}
          </Button>
        </CardFooter>
      </Card>
    </div>
  );
}
