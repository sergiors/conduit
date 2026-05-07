import { useEffect, useState } from "react";
import { Link, useParams, useNavigate } from "react-router";
import Editor from "@monaco-editor/react";

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
import { Input } from "~/components/ui/input";
import { Field, FieldLabel } from "~/components/ui/field";

import {
  ArrowLeftIcon,
  MoreHorizontalIcon,
  PlusIcon,
  PencilIcon,
  Trash2Icon,
  SaveIcon,
  XIcon,
} from "lucide-react";

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

export default function DocumentsRoute() {
  const { collectionName } = useParams<{ collectionName: string }>();
  const navigate = useNavigate();
  const [documents, setDocuments] = useState<Document[]>([]);
  const [collection, setCollection] = useState<CollectionConfig | null>(null);
  const [loading, setLoading] = useState(true);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [editingDoc, setEditingDoc] = useState<Document | null>(null);
  const [creatingDoc, setCreatingDoc] = useState(false);
  const [deletingDoc, setDeletingDoc] = useState<Document | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [jsonValue, setJsonValue] = useState("{}");

  useEffect(() => {
    fetchCollection();
  }, [collectionName]);

  useEffect(() => {
    if (collection) {
      fetchDocuments();
    }
  }, [page, collection]);

  const fetchCollection = async () => {
    try {
      const res = await fetch(`/api/collections/${collectionName}`);
      if (!res.ok) throw new Error("Failed to fetch collection");
      const data: CollectionConfig = await res.json();
      setCollection(data);
    } catch (err) {
      setError("Failed to load collection");
      console.error(err);
    }
  };

  const fetchDocuments = async () => {
    setLoading(true);
    try {
      const res = await fetch(
        `/api/collections/${collectionName}/documents?page=${page}&limit=20`,
      );
      if (!res.ok) throw new Error("Failed to fetch documents");
      const data = await res.json();
      setDocuments(data.documents || []);
      setTotal(data.total || 0);
    } catch (err) {
      setError("Failed to load documents");
      console.error(err);
    } finally {
      setLoading(false);
    }
  };

  const handleCreate = async () => {
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
        setCreatingDoc(false);
        setJsonValue("{}");
        fetchDocuments();
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

  const handleUpdate = async () => {
    if (!editingDoc) return;
    setIsSubmitting(true);
    setError(null);

    try {
      const id = editingDoc._id as string;
      const pkField = collection?.primary_key;
      const skField = collection?.sort_key;

      const params = new URLSearchParams();
      if (pkField && editingDoc[pkField]) {
        params.set("pk", String(editingDoc[pkField]));
      }
      if (skField && editingDoc[skField]) {
        params.set("sk", String(editingDoc[skField]));
      }
      if (!params.toString()) {
        params.set("id", id);
      }

      const updateData = JSON.parse(jsonValue);

      const res = await fetch(
        `/api/collections/${collectionName}/documents?${params}`,
        {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(updateData),
        },
      );

      if (res.ok) {
        setEditingDoc(null);
        setJsonValue("{}");
        fetchDocuments();
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

  const handleDelete = async () => {
    if (!deletingDoc) return;
    setIsSubmitting(true);
    setError(null);

    try {
      const id = deletingDoc._id as string;
      const pkField = collection?.primary_key;
      const skField = collection?.sort_key;

      const params = new URLSearchParams();
      if (pkField && deletingDoc[pkField]) {
        params.set("pk", String(deletingDoc[pkField]));
      }
      if (skField && deletingDoc[skField]) {
        params.set("sk", String(deletingDoc[skField]));
      }
      if (!params.toString()) {
        params.set("id", id);
      }

      const res = await fetch(
        `/api/collections/${collectionName}/documents?${params}`,
        {
          method: "DELETE",
        },
      );

      if (res.ok) {
        setDeletingDoc(null);
        fetchDocuments();
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

  const openEdit = (doc: Document) => {
    const { _id, ...rest } = doc;
    setJsonValue(JSON.stringify(rest, null, 2));
    setEditingDoc(doc);
    setCreatingDoc(false);
  };

  const openCreate = () => {
    setJsonValue("{}");
    setCreatingDoc(true);
    setEditingDoc(null);
  };

  const cancelEdit = () => {
    setEditingDoc(null);
    setCreatingDoc(false);
    setJsonValue("{}");
    setError(null);
  };

  const getDocKey = (doc: Document) => {
    const pkField = collection?.primary_key;
    const skField = collection?.sort_key;

    if (pkField && doc[pkField]) {
      const pk = doc[pkField];
      if (skField && doc[skField]) {
        return `${String(pk)}:${String(doc[skField])}`;
      }
      return String(pk);
    }
    return String(doc._id);
  };

  const getDisplayFields = () => {
    if (!collection) return ["_id"];
    const fields: string[] = [];
    if (collection.primary_key) fields.push(collection.primary_key);
    if (collection.sort_key) fields.push(collection.sort_key);
    if (fields.length === 0) fields.push("_id");
    return fields;
  };

  if (!collection) {
    return (
      <div className="p-4 md:p-8">
        <p className="text-muted-foreground">Loading...</p>
      </div>
    );
  }

  return (
    <div className="space-y-6">
      <div className="flex items-center justify-between">
        <div className="flex items-center gap-4">
          <Link to="/collections">
            <Button variant="ghost" size="icon">
              <ArrowLeftIcon className="h-4 w-4" />
            </Button>
          </Link>
          <h1 className="text-2xl font-bold">
            Documents: {collection.collection_name}
          </h1>
        </div>
        {!creatingDoc && !editingDoc && (
          <Button onClick={openCreate}>
            <PlusIcon className="h-4 w-4 mr-2" />
            New Document
          </Button>
        )}
      </div>

      {error && (
        <div className="p-4 bg-destructive/10 border border-destructive rounded-md">
          <p className="text-sm text-destructive">{error}</p>
        </div>
      )}

      {(creatingDoc || editingDoc) && (
        <Card>
          <CardContent className="p-4 space-y-4">
            <div className="flex items-center justify-between">
              <h2 className="text-lg font-semibold">
                {creatingDoc ? "Create Document" : "Edit Document"}
              </h2>
              <div className="flex gap-2">
                <Button variant="outline" size="sm" onClick={cancelEdit}>
                  <XIcon className="h-4 w-4 mr-2" />
                  Cancel
                </Button>
                <Button
                  size="sm"
                  onClick={creatingDoc ? handleCreate : handleUpdate}
                  disabled={isSubmitting}
                >
                  <SaveIcon className="h-4 w-4 mr-2" />
                  {isSubmitting
                    ? creatingDoc
                      ? "Creating..."
                      : "Updating..."
                    : "Save"}
                </Button>
              </div>
            </div>
            {editingDoc && collection && (
              <p className="text-xs text-muted-foreground">
                PK/SK fields ({collection.primary_key || "_id"}
                {collection.sort_key ? `, ${collection.sort_key}` : ""}) are
                read-only. Edit other fields below.
              </p>
            )}
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
          </CardContent>
        </Card>
      )}

      {!creatingDoc && !editingDoc && (
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
                    {getDisplayFields().map((field) => (
                      <TableHead key={field}>{field}</TableHead>
                    ))}
                    <TableHead className="w-[100px]"></TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {documents.map((doc) => (
                    <TableRow key={getDocKey(doc)}>
                      {getDisplayFields().map((field) => (
                        <TableCell key={field} className="font-medium">
                          {String(doc[field] ?? doc._id)}
                        </TableCell>
                      ))}
                      <TableCell>
                        <DropdownMenu>
                          <DropdownMenuTrigger asChild>
                            <Button variant="ghost" size="icon">
                              <MoreHorizontalIcon className="size-4" />
                            </Button>
                          </DropdownMenuTrigger>
                          <DropdownMenuContent align="end">
                            <DropdownMenuItem onClick={() => openEdit(doc)}>
                              <PencilIcon className="h-4 w-4 mr-2" />
                              Edit
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
      )}

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
