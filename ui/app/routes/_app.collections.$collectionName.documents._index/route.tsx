import { Link, useSearchParams } from "react-router";
import { ArrowRightIcon } from "lucide-react";

import { Button } from "~/components/ui/button";
import { Card, CardContent } from "~/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table";

import {
  computeColumns,
  computeKeyLabels,
  formatAttributeValue,
  isMissingValue,
} from "~/lib/document-columns";
import type { Route } from "./+types/route";
import { clientLoader } from "./loader.client";
export { clientLoader };

interface Document {
  _id: string;
  [key: string]: unknown;
}

export const handle = {
  breadcrumb: ({ params }: Route.LoaderArgs) => (
    <>
      <Link to={`/`}>{params.collectionName}</Link>
    </>
  ),
};

export default function DocumentsRoute({
  loaderData,
  params,
}: Route.ComponentProps) {
  const { collectionName } = params;
  const [searchParams, setSearchParams] = useSearchParams();

  const documents: Document[] = (loaderData?.documents || []) as Document[];
  const limit = loaderData?.limit ?? 20;
  const skip = loaderData?.skip ?? 0;

  const collection = loaderData?.collection ?? null;
  const partitionKey = collection?.partitionKey || undefined;
  const sortKey = collection?.sortKey || undefined;
  const columns = computeColumns(documents, partitionKey, sortKey);
  const keyLabels = computeKeyLabels(partitionKey, sortKey);

  const hasPrevious = skip > 0;
  // We can only know a "next" page exists if we actually received a full page.
  // When the page is filled to the limit there may be more rows, but we cannot
  // know for sure without a total count; always offer "next" when the page is
  // full and let the empty page signal the end.
  const pageIsFull = documents.length === limit;

  const changeSkip = (nextSkip: number) => {
    const next = new URLSearchParams(searchParams);
    if (nextSkip > 0) next.set("skip", String(nextSkip));
    else next.delete("skip");
    if (limit !== 20) next.set("limit", String(limit));
    setSearchParams(next, { replace: true });
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Documents: {collectionName}</h1>
      </div>

      <Card>
        <CardContent>
          {documents.length === 0 ? (
            <p className="py-8 text-center text-muted-foreground">
              No documents in this collection.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  {columns.map((column) => {
                    const label = keyLabels[column];
                    return (
                      <TableHead key={column} className="max-w-[240px]">
                        <span className="flex items-center gap-1.5">
                          {label && (
                            <span className="inline-flex items-center rounded bg-muted px-1.5 py-0.5 text-[10px] font-medium leading-none text-muted-foreground">
                              {label}
                            </span>
                          )}
                          <span className="block truncate">{column}</span>
                        </span>
                      </TableHead>
                    );
                  })}
                  <TableHead className="w-[100px]"></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {documents.map((doc: Document) => (
                  <TableRow key={doc._id}>
                    {columns.map((column) => {
                      const value = doc[column];
                      return (
                        <TableCell
                          key={column}
                          className={
                            keyLabels[column]
                              ? "font-medium text-foreground"
                              : undefined
                          }
                        >
                          {isMissingValue(value) ? (
                            <span className="text-muted-foreground/60">
                              {formatAttributeValue(value)}
                            </span>
                          ) : (
                            <span className="block max-w-[240px] truncate">
                              {formatAttributeValue(value)}
                            </span>
                          )}
                        </TableCell>
                      );
                    })}
                    <TableCell>
                      <Button
                        variant="ghost"
                        size="icon"
                        aria-label="View"
                        asChild
                      >
                        <Link to={`${String(doc._id)}`}>
                          <ArrowRightIcon className="size-4" />
                        </Link>
                      </Button>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}

          {documents.length > 0 && (
            <div className="mt-4 flex items-center justify-between">
              <Button
                variant="outline"
                onClick={() => changeSkip(skip - limit)}
                disabled={!hasPrevious}
              >
                Previous
              </Button>
              <span className="text-sm text-muted-foreground">
                Showing {documents.length} of {limit} per page
              </span>
              <Button
                variant="outline"
                onClick={() => changeSkip(skip + limit)}
                disabled={!pageIsFull}
              >
                Next
              </Button>
            </div>
          )}
        </CardContent>
      </Card>
    </div>
  );
}
