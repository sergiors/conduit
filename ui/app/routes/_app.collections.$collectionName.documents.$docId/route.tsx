import { ArrowLeftIcon } from "lucide-react";
import { useParams, Link } from "react-router";
import JsonView from "@microlink/react-json-view";
import { Button } from "~/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";

import type { Route } from "./+types/route";
export { clientLoader } from "./loader.client";

export const handle = {
  breadcrumb: ({ params }: Route.LoaderArgs) => (
    <>{params.collectionName} › View Document</>
  ),
};

export default function Route({ loaderData }: Route.ComponentProps) {
  const { collectionName } = useParams<{ collectionName: string }>();
  const document = loaderData?.document ?? null;

  return (
    <div className="space-y-6">
      <div className="flex items-center gap-4">
        <h1 className="text-2xl font-bold">Document</h1>
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Document JSON</CardTitle>
          <CardDescription className="text-xs">
            Read-only view. Documents cannot be modified through the UI.
          </CardDescription>
        </CardHeader>
        <CardContent>
          {document === null ? (
            <p className="text-sm text-destructive">Document not found</p>
          ) : (
            <div className="border rounded-2xl p-6">
              <JsonView name={null} src={document} showComma />
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
