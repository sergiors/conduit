import { useState } from "react";
import { Link, useRevalidator } from "react-router";

import { clientLoader, type TableConfig } from "./loader.client";

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
  loaderData: { tables: TableConfig[] };
}) {
  const { tables } = loaderData;
  const revalidator = useRevalidator();

  const [deletingTable, setDeletingTable] = useState<TableConfig | null>(null);
  const [isSubmitting, setIsSubmitting] = useState(false);
  const [deleteError, setDeleteError] = useState<string | null>(null);

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
    <div className="space-y-6">
      <div className="flex flex-col sm:flex-row items-start sm:items-center justify-between gap-4 mb-4">
        <h1 className="text-2xl font-bold">Tables</h1>
        <Link to="/tables/new">
          <Button>New Table</Button>
        </Link>
      </div>

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
                {tables.map((table: TableConfig) => (
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
                          <DropdownMenuItem asChild>
                            <Link to={`/tables/${table.table_name}/edit`}>
                              Edit
                            </Link>
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
