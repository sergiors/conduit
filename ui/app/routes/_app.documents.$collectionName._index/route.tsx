import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "~/components/ui/alert-dialog";
import { Button } from "~/components/ui/button";
import { Card, CardContent } from "~/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table";

import {
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  Trash2Icon,
} from "lucide-react";
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
  const navigate = useNavigate();
  const [searchParams] = useSearchParams();
  const { collectionName } = params;

  const documents: Document[] = (loaderData?.documents || []) as Document[];
  const pagination = {
    page: loaderData?.page || 1,
    limit: loaderData?.limit || 20,
    total: loaderData?.total || 0,
  };

  const [deletingDoc, setDeletingDoc] = useState<Document | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const handleDelete = async () => {
    if (!deletingDoc) return;
    setIsSubmitting(true);
    setError(null);

    try {
      const id = deletingDoc._id as string;
      const res = await fetch(
        `/api/collections/${collectionName}/documents/${id}`,
        {
          method: "DELETE",
        },
      );

      if (res.ok) {
        setDeletingDoc(null);
        navigate(`?${searchParams.toString()}`, { replace: true });
      } else {
        const error = await res.json();
        setError(error.error || "Failed to delete document");
      }
    } catch (err) {
      setError("Failed to delete document");
      console.error(err);
    } finally {
      setIsSubmitting(false);
    }
  };

  const changePage = (newPage: number) => {
    const newParams = new URLSearchParams(searchParams);
    newParams.set("page", String(newPage));
    navigate(`?${newParams.toString()}`, { replace: true });
  };

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <h1 className="text-2xl font-bold">Documents: {collectionName}</h1>
      </div>

      {error && (
        <div className="p-4 bg-destructive/10 border border-destructive rounded-md">
          <p className="text-sm text-destructive">{error}</p>
        </div>
      )}

      <Card>
        <CardContent>
          {documents.length === 0 ? (
            <p className="text-muted-foreground text-center py-8">
              No documents in this collection.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead className="w-[100px]"></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {documents.map((doc: Document) => (
                  <TableRow key={doc._id}>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon">
                            <MoreHorizontalIcon className="size-4" />
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent align="end">
                          <DropdownMenuItem asChild>
                            <Link to={`${String(doc._id)}/edit`}>
                              <PencilIcon className="h-4 w-4 mr-2" />
                              Edit
                            </Link>
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => setDeletingDoc(doc)}
                            className="text-destructive"
                          >
                            <Trash2Icon className="h-4 w-4 mr-2" />
                            Delete
                          </DropdownMenuItem>
                        </DropdownMenuContent>
                      </DropdownMenu>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          )}

          {documents.length > 0 && (
            <div className="flex items-center justify-between mt-4">
              <Button
                variant="outline"
                onClick={() => changePage(pagination.page - 1)}
                disabled={pagination.page === 1}
              >
                Previous
              </Button>
              <span className="text-sm text-muted-foreground">
                Page {pagination.page} of{" "}
                {Math.ceil(pagination.total / pagination.limit)} (
                {pagination.total} total)
              </span>
              <Button
                variant="outline"
                onClick={() => changePage(pagination.page + 1)}
                disabled={
                  pagination.page * pagination.limit >= pagination.total
                }
              >
                Next
              </Button>
            </div>
          )}
        </CardContent>
      </Card>

      {/* Delete Confirmation Dialog */}
      <AlertDialog
        open={!!deletingDoc}
        onOpenChange={(open) => !open && setDeletingDoc(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Document</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete this document? This action cannot
              be undone.
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <AlertDialogAction
              variant="destructive"
              onClick={handleDelete}
              disabled={isSubmitting}
            >
              {isSubmitting ? "Deleting..." : "Delete"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </div>
  );
}
