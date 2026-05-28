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
import { formSchema, type FormData } from "./schema";
export { clientAction } from "./action.client";

export const handle = {
  breadcrumb: () => <>New Collection</>,
};

export default function NewCollectionRoute() {
  const submit = useSubmit();
  const actionData = useActionData();

  const {
    control,
    handleSubmit,
    formState: { errors, isSubmitting },
    reset,
    watch,
  } = useForm<FormData>({
    resolver: zodResolver(formSchema),
    defaultValues: {
      collection_name: "",
      composite_keys: false,
      partition_key: "",
      sort_key: "",
      deletion_protection: true,
    } as FormData,
  });

  const compositeKeys = watch("composite_keys");

  const onSubmit = async (data: FormData) => {
    await submit(data, {
      method: "post",
      action: ".",
      encType: "application/json",
    });

    reset();
  };

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">New Collection</h1>

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
                  Collection created successfully!
                </AlertDescription>
              </Alert>
            )}

            <FieldGroup>
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
                      placeholder="e.g., users, orders"
                    />
                    <FieldError errors={[errors.collection_name]} />
                  </Field>
                )}
              />

              <Field orientation="horizontal" className="items-start">
                <Controller
                  name="composite_keys"
                  control={control}
                  render={({ field }) => (
                    <FieldLabel className="has-data-checked:bg-transparent">
                      <Checkbox
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                      <span>Use composite keys</span>
                    </FieldLabel>
                  )}
                />
              </Field>

              {compositeKeys && (
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4 border rounded-4xl p-6">
                  <Controller
                    name="partition_key"
                    control={control}
                    render={({ field, fieldState }) => (
                      <Field data-invalid={fieldState.invalid}>
                        <FieldLabel htmlFor={field.name}>
                          Partition key *
                        </FieldLabel>
                        <Input
                          {...field}
                          id={field.name}
                          aria-invalid={fieldState.invalid}
                          placeholder="e.g., pk, id, userId"
                        />
                        <FieldError errors={[errors.partition_key]} />
                      </Field>
                    )}
                  />

                  <Controller
                    name="sort_key"
                    control={control}
                    render={({ field, fieldState }) => (
                      <Field data-invalid={fieldState.invalid}>
                        <FieldLabel htmlFor={field.name}>Sort key</FieldLabel>
                        <Input
                          {...field}
                          id={field.name}
                          aria-invalid={fieldState.invalid}
                          placeholder="e.g., sk, sort, createdAt"
                        />
                        <FieldError errors={[errors.sort_key]} />
                      </Field>
                    )}
                  />
                </div>
              )}
            </FieldGroup>

            <div className="flex gap-2 justify-end">
              <Button type="button" variant="outline" asChild>
                <Link to="/collections">Cancel</Link>
              </Button>
              <Button type="submit" disabled={isSubmitting}>
                {isSubmitting ? "Creating..." : "Create Collection"}
              </Button>
            </div>
          </Form>
        </CardContent>
      </Card>
    </div>
  );
}
