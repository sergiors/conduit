import { useState } from "react";
import { Link, useRevalidator } from "react-router";

import { clientLoader, type CollectionConfig } from "./loader.client";

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
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "~/components/ui/dialog";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "~/components/ui/table";

import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";

import { MoreHorizontalIcon } from "lucide-react";

export { clientLoader };

export function meta() {
  return [
    { title: "Tables - Conduit" },
    { name: "description", content: "Conduit Tables Management" },
  ];
}

export default function Route({
  loaderData,
}: {
  loaderData: { collections: CollectionConfig[] };
}) {
  const { collections } = loaderData;
  const revalidator = useRevalidator();

  const [deletingCollection, setDeletingCollection] = useState<CollectionConfig | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const handleDelete = async () => {
    if (!deletingCollection?.collection_name) return;

    setIsSubmitting(true);
    setDeleteError(null);
    try {
      const res = await fetch(`/api/collections/${deletingCollection.collection_name}`, {
        method: "DELETE",
      });

      if (res.ok) {
        setDeletingCollection(null);
        revalidator.revalidate();
      } else {
        const error = await res.json();
        setDeleteError(error.error || "Failed to delete collection");
      }
    } catch (err) {
      setDeleteError("Failed to delete collection");
      console.error("Failed to delete collection:", err);
    } finally {
      setIsSubmitting(false);
    }
  };

  const openDeleteDialog = (collection: CollectionConfig) => {
    if (collection.deletion_protection) return;
    setDeletingCollection(collection);
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 mb-4">
        <h1 className="text-2xl font-bold">Collections</h1>
        <Link to="/tables/new">
          <Button>New Collection</Button>
        </Link>
      </div>

      {/* Delete Confirmation Dialog */}
      <AlertDialog
        open={!!deletingCollection}
        onOpenChange={(open) => !open && setDeletingCollection(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Collection</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete the collection{" "}
              <span className="font-medium text-foreground">
                {deletingCollection?.collection_name}
              </span>
              ? This action cannot be undone.
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
          {deleteError && (
            <p className="text-sm text-destructive text-center">
              {deleteError}
            </p>
          )}
        </AlertDialogContent>
      </AlertDialog>

      <Card>
        <CardContent>
          {collections.length === 0 ? (
            <p className="text-muted-foreground">No collections configured.</p>
          ) : (
            <Table>
              <TableHeader className="pointer-events-none">
                <TableRow>
                  <TableHead>Name</TableHead>
                  <TableHead>Stream</TableHead>
                  <TableHead>Old Image</TableHead>
                  <TableHead>TTL Attribute</TableHead>
                  <TableHead>Destinations</TableHead>
                  <TableHead>Deletion Protection</TableHead>
                  <TableHead className="w-[50px]"></TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {collections.map((collection: CollectionConfig) => (
                  <TableRow key={collection._id || collection.collection_name}>
                    <TableCell className="font-medium">
                      {collection.collection_name}
                    </TableCell>
                    <TableCell>
                      <span
                        className={`text-sm ${
                          collection.stream_enabled
                            ? "text-green-600"
                            : "text-muted-foreground"
                        }`}
                      >
                        {collection.stream_enabled ? "Yes" : "No"}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span
                        className={`text-sm ${
                          collection.old_image
                            ? "text-green-600"
                            : "text-muted-foreground"
                        }`}
                      >
                        {collection.old_image ? "Yes" : "No"}
                      </span>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {collection.ttl_attribute || "-"}
                    </TableCell>
                    <TableCell>
                      <DestinationsCell destinations={collection.destinations} />
                    </TableCell>
                    <TableCell>
                      <span
                        className={`text-sm ${
                          collection.deletion_protection
                            ? "text-green-600"
                            : "text-red-600"
                        }`}
                      >
                        {collection.deletion_protection ? "Enabled" : "Disabled"}
                      </span>
                    </TableCell>
                    <TableCell>
                      <DropdownMenu>
                        <DropdownMenuTrigger asChild>
                          <Button variant="ghost" size="icon">
                            <MoreHorizontalIcon className="size-4" />
                            <span className="sr-only">Actions</span>
                          </Button>
                        </DropdownMenuTrigger>
                        <DropdownMenuContent
                          align="end"
                          className="[&_[role='menuitem']]:cursor-pointer"
                        >
                          <DropdownMenuItem asChild>
                            <Link to={`/tables/${collection.collection_name}/edit`}>
                              Edit
                            </Link>
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => openDeleteDialog(collection)}
                            disabled={collection.deletion_protection}
                            className="text-destructive focus:text-destructive"
                          >
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
        </CardContent>
      </Card>
    </div>
  );
}

function DestinationsCell({
  destinations,
}: {
  destinations: CollectionConfig["destinations"];
}) {
  const [open, setOpen] = useState(false);

  return (
    <>
      <Button variant="outline" size="sm" onClick={() => setOpen(true)}>
        {destinations.length}{" "}
        {destinations.length === 1 ? "destination" : "destinations"}
      </Button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>Destinations</DialogTitle>
          </DialogHeader>
          <div className="space-y-4">
            {destinations.map((d, i) => (
              <div key={i} className="border rounded-lg p-4 space-y-2">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{d.type}</span>
                  {d.endpoint && (
                    <span className="text-muted-foreground text-sm">
                      → {d.endpoint}
                    </span>
                  )}
                </div>
                {d.bearer_token && (
                  <div className="text-sm text-muted-foreground">
                    <span className="font-medium">Bearer Token:</span>{" "}
                    {d.bearer_token.substring(0, 20)}...
                  </div>
                )}
                <div className="text-sm">
                  <span className="font-medium">Event Types:</span>{" "}
                  <span className="text-muted-foreground">
                    {d.event_types?.join(", ") || "ALL"}
                  </span>
                </div>
              </div>
            ))}
          </div>
        </DialogContent>
      </Dialog>
    </>
  );
}
