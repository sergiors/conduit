import { DatabaseIcon, MoreHorizontalIcon, PlusIcon } from "lucide-react";
import { useState } from "react";
import { Link, useRevalidator, useRouteLoaderData } from "react-router";

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
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "~/components/ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "~/components/ui/dropdown-menu";

import type { CollectionConfig } from "../_app/loader.client";
import type { Route } from "./+types/route";

export const handle = {
  breadcrumb: ({}: Route.LoaderArgs) => <>Collections</>,
};

// Tipo do loader do _app/route.tsx
type AppLoaderData = { collections: CollectionConfig[] };

export default function Route() {
  const loaderData = useRouteLoaderData("routes/_app") as
    | AppLoaderData
    | undefined;
  const collections = loaderData?.collections || [];

  const revalidator = useRevalidator();

  const [deletingCollection, setDeletingCollection] =
    useState<CollectionConfig | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const handleDelete = async () => {
    if (!deletingCollection?.collection_name) return;

    setIsSubmitting(true);
    setDeleteError(null);
    try {
      const res = await fetch(
        `/api/collections/${deletingCollection.collection_name}`,
        {
          method: "DELETE",
        },
      );

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
    if (collection.deletion_protection) {
      setDeleteError("Disable deletion protection first");
      return;
    }
    setDeletingCollection(collection);
  };

  return (
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4">
        <div>
          <h1 className="text-2xl font-bold">Collections</h1>
          <p className="text-sm text-muted-foreground">
            Manage your MongoDB collections and CDC configurations
          </p>
        </div>
        <Button size="sm" asChild>
          <Link to="/collections/new">
            <PlusIcon /> New Collection
          </Link>
        </Button>
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

      {collections.length === 0 ? (
        <Card>
          <CardHeader className="text-center">
            <DatabaseIcon className="h-12 w-12 mx-auto text-muted-foreground" />
            <CardTitle>No collections yet</CardTitle>
            <CardDescription>
              Create your first collection to start managing MongoDB change data
              capture
            </CardDescription>
          </CardHeader>
          <CardContent className="flex justify-center">
            <Button asChild>
              <Link to="/collections/new">
                <PlusIcon /> Create Collection
              </Link>
            </Button>
          </CardContent>
        </Card>
      ) : (
        <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
          {collections.map((collection: CollectionConfig) => (
            <Card
              key={collection._id || collection.collection_name}
              className="relative"
            >
              <CardHeader>
                <div className="flex items-start justify-between gap-2">
                  <div className="flex items-center gap-2">
                    <DatabaseIcon className="h-5 w-5 text-muted-foreground" />
                    <CardTitle className="text-lg">
                      {collection.collection_name}
                    </CardTitle>
                  </div>
                  <DropdownMenu>
                    <DropdownMenuTrigger asChild>
                      <Button variant="ghost" size="icon" className="h-8 w-8">
                        <MoreHorizontalIcon className="h-4 w-4" />
                        <span className="sr-only">Actions</span>
                      </Button>
                    </DropdownMenuTrigger>
                    <DropdownMenuContent align="end">
                      <DropdownMenuItem asChild>
                        <Link to={`/documents/${collection.collection_name}`}>
                          Documents
                        </Link>
                      </DropdownMenuItem>
                      <DropdownMenuItem
                        disabled={!collection.stream_enabled}
                        asChild
                      >
                        <Link
                          to={`/collections/${collection.collection_name}/destinations`}
                        >
                          Destinations
                        </Link>
                      </DropdownMenuItem>
                      <DropdownMenuSeparator />
                      <DropdownMenuItem asChild>
                        <Link
                          to={`/collections/${collection.collection_name}/settings`}
                        >
                          Settings
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
                </div>
                <CardDescription>
                  {collection.partition_key && (
                    <span className="mr-3">
                      PK:{" "}
                      <code className="text-xs bg-muted px-1 rounded">
                        {collection.partition_key}
                      </code>
                    </span>
                  )}
                  {collection.sort_key && (
                    <span>
                      SK:{" "}
                      <code className="text-xs bg-muted px-1 rounded">
                        {collection.sort_key}
                      </code>
                    </span>
                  )}
                </CardDescription>
              </CardHeader>
              <CardContent>
                <div className="grid grid-cols-2 gap-2 text-xs">
                  <div className="flex items-center gap-2">
                    <div
                      className={`h-2 w-2 rounded-full ${collection.stream_enabled ? "bg-green-500" : "bg-muted"}`}
                    />
                    <span className="text-muted-foreground">Stream</span>
                    <span className="font-medium">
                      {collection.stream_enabled ? "On" : "Off"}
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <div
                      className={`h-2 w-2 rounded-full ${collection.old_image ? "bg-blue-500" : "bg-muted"}`}
                    />
                    <span className="text-muted-foreground">Old Image</span>
                    <span className="font-medium">
                      {collection.old_image ? "Yes" : "No"}
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="text-muted-foreground">TTL</span>
                    <span className="font-medium">
                      {collection.ttl_attribute || "-"}
                    </span>
                  </div>
                  <div className="flex items-center gap-2">
                    <span className="text-muted-foreground">Protection</span>
                    <span
                      className={`font-medium ${collection.deletion_protection ? "text-amber-600" : "text-green-600"}`}
                    >
                      {collection.deletion_protection ? "On" : "Off"}
                    </span>
                  </div>
                </div>
                {(collection.destinations || []).length > 0 && (
                  <div className="mt-3 pt-3 border-t">
                    <div className="flex items-center gap-1 flex-wrap">
                      {(collection.destinations || []).map((dest, idx) => (
                        <span
                          key={idx}
                          className="text-xs bg-secondary text-secondary-foreground px-2 py-0.5 rounded-full"
                        >
                          {dest.type}
                        </span>
                      ))}
                    </div>
                  </div>
                )}
              </CardContent>
            </Card>
          ))}
        </div>
      )}
    </div>
  );
}
