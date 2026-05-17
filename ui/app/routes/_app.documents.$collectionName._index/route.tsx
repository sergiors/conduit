import { useEffect, useState } from "react";
import { Link, useParams } from "react-router";

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
  ArrowLeftIcon,
  MoreHorizontalIcon,
  PencilIcon,
  PlusIcon,
  Trash2Icon,
} from "lucide-react";
import type { Route } from "./+types/route";

interface Document {
  _id: string;
  [key: string]: unknown;
}

interface CollectionConfig {
  _id?: string;
  collection_name: string;
  primary_key?: string;
  sort_key?: string;
}

export { clientLoader } from "./loader.client";

export default function DocumentsRoute({ loaderData }: Route.ComponentProps) {
  const { documents } = loaderData;
  const { collectionName } = useParams<{ collectionName: string }>();
  // const [documents, setDocuments] = useState<Document[]>([]);
  // const [collection, setCollection] = useState<CollectionConfig | null>(null);
  const [loading, setLoading] = useState(false);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [deletingDoc, setDeletingDoc] = useState<Document | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);

  console.log(documents);

  // useEffect(() => {
  //   fetchCollection();
  // }, [collectionName]);

  // useEffect(() => {
  //   if (collection) {
  //     fetchDocuments();
  //   }
  // }, [page, collection]);

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
        // fetchDocuments();
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

  // const getDocKey = (doc: Document) => {
  //   const pkField = collection?.primary_key;
  //   const skField = collection?.sort_key;

  //   if (pkField && doc[pkField]) {
  //     const pk = doc[pkField];
  //     if (skField && doc[skField]) {
  //       return `${String(pk)}:${String(doc[skField])}`;
  //     }
  //     return String(pk);
  //   }
  //   return String(doc._id);
  // };

  // const getDisplayFields = () => {
  //   if (!collection) return ["_id"];
  //   const fields: string[] = [];
  //   if (collection.primary_key) fields.push(collection.primary_key);
  //   if (collection.sort_key) fields.push(collection.sort_key);
  //   if (fields.length === 0) fields.push("_id");
  //   return fields;
  // };

  // if (!collection) {
  //   return (
  //     <div className="p-4 md:p-8">
  //       <p className="text-muted-foreground">Loading...</p>
  //     </div>
  //   );
  // }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-4">
          <Link to="/collections">
            <Button variant="ghost" size="icon">
              <ArrowLeftIcon className="h-4 w-4" />
            </Button>
          </Link>
          <h1 className="text-2xl font-bold">Documents: {collectionName}</h1>
        </div>
        <Link to="new">
          <Button>
            <PlusIcon className="h-4 w-4 mr-2" />
            New Document
          </Button>
        </Link>
      </div>

      {error && (
        <div className="p-4 bg-destructive/10 border border-destructive rounded-md">
          <p className="text-sm text-destructive">{error}</p>
        </div>
      )}

      <Card>
        <CardContent>
          {loading ? (
            <p className="text-muted-foreground text-center py-8">
              Loading documents...
            </p>
          ) : documents.length === 0 ? (
            <p className="text-muted-foreground text-center py-8">
              No documents in this collection.
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  {/*             {getDisplayFields().map((field) => (
                    <TableHead key={field}>{field}</TableHead>
                  ))}*/}
                  <TableHead className="w-[100px]"></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {documents.map((doc: Document) => (
                  <TableRow key={doc._id}>
                    {/*{getDisplayFields().map((field) => (
                      <TableCell key={field} className="font-medium">
                        {String(doc[field] ?? doc._id)}
                      </TableCell>
                    ))}*/}
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
                onClick={() => setPage((p) => Math.max(1, p - 1))}
                disabled={page === 1}
              >
                Previous
              </Button>
              <span className="text-sm text-muted-foreground">
                Page {page} of {Math.ceil(total / 20)}
              </span>
              <Button
                variant="outline"
                onClick={() => setPage((p) => p + 1)}
                disabled={page * 20 >= total}
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
