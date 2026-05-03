import { useState } from "react";
import { useRevalidator } from "react-router";

import type { Route } from "./+types/route";
import { clientLoader, type TableConfig } from "./loader.client";
import { TableForm } from "./table-form";

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
  DialogTrigger,
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

export function meta({}: Route.MetaArgs) {
  return [
    { title: "Tables - Relay" },
    { name: "description", content: "Relay Tables Management" },
  ];
}

export default function Route({ loaderData }: Route.ComponentProps) {
  const { tables } = loaderData;
  const revalidator = useRevalidator();

  const [createDialogOpen, setCreateDialogOpen] = useState(false);
  const [editingTable, setEditingTable] = useState<TableConfig | null>(null);
  const [deletingTable, setDeletingTable] = useState<TableConfig | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [createError, setCreateError] = useState<string | null>(null);
  const [editError, setEditError] = useState<string | null>(null);
  const [deleteError, setDeleteError] = useState<string | null>(null);

  const handleCreate = async (data: TableConfig) => {
    setIsSubmitting(true);
    setCreateError(null);
    try {
      const res = await fetch("/api/tables", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      });

      if (res.ok) {
        setCreateDialogOpen(false);
        revalidator.revalidate();
      } else {
        const error = await res.json();
        setCreateError(error.error || "Failed to create table");
      }
    } catch (err) {
      setCreateError("Failed to create table");
      console.error("Failed to create table:", err);
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleUpdate = async (data: TableConfig) => {
    if (!editingTable?.table_name) return;

    setIsSubmitting(true);
    setEditError(null);
    try {
      const res = await fetch(`/api/tables/${editingTable.table_name}`, {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(data),
      });

      if (res.ok) {
        setEditingTable(null);
        revalidator.revalidate();
      } else {
        const error = await res.json();
        setEditError(error.error || "Failed to update table");
      }
    } catch (err) {
      setEditError("Failed to update table");
      console.error("Failed to update table:", err);
    } finally {
      setIsSubmitting(false);
    }
  };

  const handleDelete = async () => {
    if (!deletingTable?.table_name) return;

    setIsSubmitting(true);
    setDeleteError(null);
    try {
      const res = await fetch(`/api/tables/${deletingTable.table_name}`, {
        method: "DELETE",
      });

      if (res.ok) {
        setDeletingTable(null);
        revalidator.revalidate();
      } else {
        const error = await res.json();
        setDeleteError(error.error || "Failed to delete table");
      }
    } catch (err) {
      setDeleteError("Failed to delete table");
      console.error("Failed to delete table:", err);
    } finally {
      setIsSubmitting(false);
    }
  };

  const openDeleteDialog = (table: TableConfig) => {
    if (table.deletion_protection) return;
    setDeletingTable(table);
  };

  return (
    <div className="p-4 md:p-8">
      <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 mb-4">
        <h1 className="text-2xl font-bold">Tables</h1>
        <Dialog open={createDialogOpen} onOpenChange={setCreateDialogOpen}>
          <DialogTrigger asChild>
            <Button>New Table</Button>
          </DialogTrigger>
          <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
            <TableForm
              onSubmit={handleCreate}
              onCancel={() => setCreateDialogOpen(false)}
              isSubmitting={isSubmitting}
            />
            {createError && (
              <p className="text-sm text-destructive text-center">
                {createError}
              </p>
            )}
          </DialogContent>
        </Dialog>
      </div>

      {/* Edit Dialog */}
      <Dialog
        open={!!editingTable}
        onOpenChange={(open) => !open && setEditingTable(null)}
      >
        <DialogContent className="max-w-2xl max-h-[90vh] overflow-y-auto">
          <TableForm
            initialData={editingTable || undefined}
            onSubmit={handleUpdate}
            onCancel={() => setEditingTable(null)}
            isSubmitting={isSubmitting}
          />
          {editError && (
            <p className="text-sm text-destructive text-center">{editError}</p>
          )}
        </DialogContent>
      </Dialog>

      {/* Delete Confirmation Dialog */}
      <AlertDialog
        open={!!deletingTable}
        onOpenChange={(open) => !open && setDeletingTable(null)}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>Delete Table</AlertDialogTitle>
            <AlertDialogDescription>
              Are you sure you want to delete the table{" "}
              <span className="font-medium text-foreground">
                {deletingTable?.table_name}
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
          {tables.length === 0 ? (
            <p className="text-muted-foreground">No tables configured.</p>
          ) : (
            <Table>
              <TableHeader>
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
                {tables.map((table) => (
                  <TableRow key={table._id || table.table_name}>
                    <TableCell className="font-medium">
                      {table.table_name}
                    </TableCell>
                    <TableCell>
                      <span
                        className={`text-sm ${
                          table.stream_enabled
                            ? "text-green-600"
                            : "text-muted-foreground"
                        }`}
                      >
                        {table.stream_enabled ? "Yes" : "No"}
                      </span>
                    </TableCell>
                    <TableCell>
                      <span
                        className={`text-sm ${
                          table.old_image
                            ? "text-green-600"
                            : "text-muted-foreground"
                        }`}
                      >
                        {table.old_image ? "Yes" : "No"}
                      </span>
                    </TableCell>
                    <TableCell className="text-muted-foreground">
                      {table.ttl_attribute || "-"}
                    </TableCell>
                    <TableCell>
                      <DestinationsCell destinations={table.destinations} />
                    </TableCell>
                    <TableCell>
                      <span
                        className={`text-sm ${
                          table.deletion_protection
                            ? "text-green-600"
                            : "text-red-600"
                        }`}
                      >
                        {table.deletion_protection ? "Enabled" : "Disabled"}
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
                          <DropdownMenuItem
                            onClick={() => setEditingTable(table)}
                          >
                            Edit
                          </DropdownMenuItem>
                          <DropdownMenuItem
                            onClick={() => openDeleteDialog(table)}
                            disabled={table.deletion_protection}
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
  destinations: TableConfig["destinations"];
}) {
  return (
    <Dialog>
      <DialogTrigger asChild>
        <Button variant="outline" size="sm">
          {destinations.length}{" "}
          {destinations.length === 1 ? "destination" : "destinations"}
        </Button>
      </DialogTrigger>
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
  );
}
