import { zodResolver } from "@hookform/resolvers/zod";
import { Controller, useForm } from "react-hook-form";
import { Form, Link, useActionData, useSubmit } from "react-router";

import { Button } from "~/components/ui/button";
import { Card, CardContent } from "~/components/ui/card";
import { Checkbox } from "~/components/ui/checkbox";
import {
  Field,
  FieldError,
  FieldGroup,
  FieldLabel,
} from "~/components/ui/field";
import { Input } from "~/components/ui/input";

import { AlertCircleIcon } from "lucide-react";
import { Alert, AlertDescription } from "~/components/ui/alert";
import type { Route } from "./+types/route";
import { collectionFormSchema, type CollectionForm } from "./schema";
export { clientAction } from "./action.client";
export { clientLoader } from "./loader.client";

export const handle = {
  breadcrumb: ({ params }: Route.LoaderArgs) => (
    <>{params.collectionName} › Settings</>
  ),
};

export default function EditCollectionRoute({
  params,
  loaderData,
}: Route.ComponentProps) {
  const { collectionName } = params;
  const { collection } = loaderData;
  const submit = useSubmit();
  const actionData = useActionData();

  if (!collection) {
    return (
      <div className="p-4 md:p-8">
        <p className="text-destructive">Collection not found</p>
      </div>
    );
  }

  const {
    control,
    handleSubmit,
    formState: { errors, isSubmitting },
    watch,
  } = useForm<CollectionForm>({
    resolver: zodResolver(collectionFormSchema),
    defaultValues: {
      collection_name: collection?.collection_name ?? "",
      partition_key: collection?.partition_key ?? "",
      sort_key: collection?.sort_key ?? "",
      stream_enabled: collection?.stream_enabled ?? false,
      old_image: collection?.old_image ?? false,
      ttl_attribute: collection?.ttl_attribute ?? "",
      deletion_protection: collection?.deletion_protection ?? true,
    },
  });

  const streamEnabled = watch("stream_enabled");

  const onSubmit = async (data: CollectionForm) => {
    await submit(data, {
      method: "put",
      action: ".",
      encType: "application/json",
    });
  };

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">Settings: {collectionName}</h1>

      <Card>
        <CardContent>
          <Form onSubmit={handleSubmit(onSubmit)} className="space-y-6">
            {actionData?.error && (
              <Alert variant="destructive">
                <AlertCircleIcon />
                <AlertDescription>{actionData.error}</AlertDescription>
              </Alert>
            )}

            {actionData?.success && (
              <Alert className="bg-green-500">
                <AlertDescription className="text-foreground">
                  Collection updated successfully!
                </AlertDescription>
              </Alert>
            )}

            <FieldGroup>
              <div className="grid grid-cols-1 md:grid-cols-3 gap-4">
                <Controller
                  name="collection_name"
                  control={control}
                  render={({ field, fieldState }) => (
                    <Field data-invalid={fieldState.invalid}>
                      <FieldLabel htmlFor={field.name}>
                        Collection Name *
                      </FieldLabel>
                      <Input
                        {...field}
                        id={field.name}
                        aria-invalid={fieldState.invalid}
                        disabled
                      />
                      <FieldError errors={[errors.collection_name]} />
                    </Field>
                  )}
                />

                <Controller
                  name="partition_key"
                  control={control}
                  render={({ field }) => (
                    <Field>
                      <FieldLabel htmlFor={field.name}>
                        Partition Key
                      </FieldLabel>
                      <Input {...field} id={field.name} disabled />
                      {collection?.partition_key && (
                        <p className="text-xs text-muted-foreground mt-1">
                          Cannot be changed after creation
                        </p>
                      )}
                    </Field>
                  )}
                />

                <Controller
                  name="sort_key"
                  control={control}
                  render={({ field }) => (
                    <Field>
                      <FieldLabel htmlFor={field.name}>Sort Key</FieldLabel>
                      <Input {...field} id={field.name} disabled />
                      {collection?.sort_key && (
                        <p className="text-xs text-muted-foreground mt-1">
                          Cannot be changed after creation
                        </p>
                      )}
                    </Field>
                  )}
                />
              </div>

              <div className="grid grid-cols-1 md:grid-cols-2 gap-4">
                <Controller
                  name="ttl_attribute"
                  control={control}
                  render={({ field }) => (
                    <Field>
                      <FieldLabel htmlFor={field.name}>
                        TTL Attribute
                      </FieldLabel>
                      <Input {...field} id={field.name} disabled />
                      {collection?.ttl_attribute && (
                        <p className="text-xs text-muted-foreground mt-1">
                          Cannot be changed after creation
                        </p>
                      )}
                    </Field>
                  )}
                />
              </div>

              <div className="flex flex-col gap-4 md:flex-row md:flex-wrap">
                <Field orientation="horizontal">
                  <Controller
                    name="stream_enabled"
                    control={control}
                    render={({ field }) => (
                      <FieldLabel className="has-data-checked:bg-transparent">
                        <Checkbox
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                        <span>Stream Enabled</span>
                      </FieldLabel>
                    )}
                  />
                </Field>

                <Field orientation="horizontal">
                  <Controller
                    name="old_image"
                    control={control}
                    render={({ field }) => (
                      <FieldLabel className="has-data-checked:bg-transparent">
                        <Checkbox
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                        <span>Old Image</span>
                      </FieldLabel>
                    )}
                  />
                </Field>

                <Field orientation="horizontal">
                  <Controller
                    name="deletion_protection"
                    control={control}
                    render={({ field }) => (
                      <FieldLabel className="has-data-checked:bg-transparent">
                        <Checkbox
                          checked={field.value}
                          onCheckedChange={field.onChange}
                        />
                        <span>Deletion Protection</span>
                      </FieldLabel>
                    )}
                  />
                </Field>
              </div>

              {streamEnabled && (
                <p className="text-sm text-muted-foreground">
                  Configure sinks in the{" "}
                  <Link
                    to={`/collections/${collectionName}/sinks`}
                    className="underline"
                  >
                    Sinks page
                  </Link>
                </p>
              )}
            </FieldGroup>

            <div className="flex gap-2 justify-end">
              <Button type="button" variant="outline" asChild>
                <Link to="/collections">Cancel</Link>
              </Button>
              <Button type="submit" disabled={isSubmitting}>
                {isSubmitting ? "Updating..." : "Update Collection"}
              </Button>
            </div>
          </Form>
        </CardContent>
      </Card>
    </div>
  );
}
