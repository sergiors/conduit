import { zodResolver } from "@hookform/resolvers/zod";
import { useState } from "react";
import { useNavigate } from "react-router";
import { Controller, useForm } from "react-hook-form";

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

import { createCollectionSchema, type CreateCollectionInput } from "./schemas";
import { clientAction } from "./action.client";

export { clientAction };

export default function NewCollectionRoute() {
  const navigate = useNavigate();
  const [submitError, setSubmitError] = useState<string | null>(null);

  const {
    control,
    handleSubmit,
    formState: { errors, isSubmitting },
    watch,
  } = useForm<CreateCollectionInput>({
    resolver: zodResolver(createCollectionSchema),
    defaultValues: {
      collection_name: "",
      use_dynamodb_mode: false,
      primary_key: "",
      sort_key: "",
    },
  });

  const useDynamoMode = watch("use_dynamodb_mode");

  const onSubmit = async (data: CreateCollectionInput) => {
    setSubmitError(null);

    const formData = new FormData();
    formData.append("collection_name", data.collection_name);
    formData.append("use_dynamodb_mode", String(data.use_dynamodb_mode));
    if (data.use_dynamodb_mode) {
      formData.append("primary_key", data.primary_key || "");
      formData.append("sort_key", data.sort_key || "");
    }

    const result = await clientAction({ request: new Request("http://localhost", {
      method: "POST",
      body: formData,
    })});

    if (result.error) {
      setSubmitError(result.error);
    } else {
      navigate("/collections");
    }
  };

  return (
    <div className="space-y-6">
      <h1 className="text-2xl font-bold">New Collection</h1>

      <Card>
        <CardContent className="pt-6">
          <form onSubmit={handleSubmit(onSubmit)} className="space-y-6">
            <FieldGroup className="space-y-6">
              <Field>
                <FieldLabel>Collection Name *</FieldLabel>
                <Controller
                  name="collection_name"
                  control={control}
                  render={({ field }) => (
                    <Input {...field} placeholder="e.g., users, orders" />
                  )}
                />
                <FieldError errors={[errors.collection_name]} />
              </Field>

              <Field orientation="horizontal" className="items-start">
                <Controller
                  name="use_dynamodb_mode"
                  control={control}
                  render={({ field }) => (
                    <FieldLabel className="has-data-checked:bg-transparent flex items-center gap-2 cursor-pointer">
                      <Checkbox
                        checked={field.value}
                        onCheckedChange={field.onChange}
                      />
                      <span>Use DynamoDB-style keys (pk/sk)</span>
                    </FieldLabel>
                  )}
                />
              </Field>

              {useDynamoMode && (
                <div className="grid grid-cols-1 md:grid-cols-2 gap-4 pl-4 border-l-2 border-border">
                  <Field>
                    <FieldLabel>Primary Key (pk) *</FieldLabel>
                    <Controller
                      name="primary_key"
                      control={control}
                      render={({ field }) => (
                        <Input {...field} placeholder="e.g., pk, id, userId" />
                      )}
                    />
                    <FieldError errors={[errors.primary_key]} />
                  </Field>

                  <Field>
                    <FieldLabel>Sort Key (sk)</FieldLabel>
                    <Controller
                      name="sort_key"
                      control={control}
                      render={({ field }) => (
                        <Input {...field} placeholder="e.g., sk, sort, createdAt" />
                      )}
                    />
                    <FieldError errors={[errors.sort_key]} />
                  </Field>
                </div>
              )}

              {submitError && (
                <p className="text-sm text-destructive">{submitError}</p>
              )}
            </FieldGroup>

            <div className="flex gap-2 justify-end">
              <Button
                type="button"
                variant="outline"
                onClick={() => navigate("/collections")}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={isSubmitting}>
                {isSubmitting ? "Creating..." : "Create Collection"}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}