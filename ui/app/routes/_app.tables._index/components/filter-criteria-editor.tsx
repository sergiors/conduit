import { PlusIcon, Trash2Icon } from "lucide-react";
import { Controller, useFieldArray, type Control } from "react-hook-form";

import { Button } from "~/components/ui/button";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "~/components/ui/select";
import { Input } from "~/components/ui/input";
import { Separator } from "~/components/ui/separator";

import type { TableForm } from "./types";

// Condition types
type ConditionType = "prefix" | "suffix" | "exists" | "numeric" | "anything-but";

const conditionOptions: { value: ConditionType; label: string }[] = [
  { value: "prefix", label: "Prefix" },
  { value: "suffix", label: "Suffix" },
  { value: "exists", label: "Exists" },
  { value: "numeric", label: "Numeric" },
  { value: "anything-but", label: "Anything But" },
];

const numericOperators = [
  { value: ">", label: ">" },
  { value: "<", label: "<" },
  { value: ">=", label: ">=" },
  { value: "<=", label: "<=" },
  { value: "=", label: "=" },
];

interface FilterCriteriaEditorProps {
  imageType: "old_image" | "new_image";
  destIndex: number;
  control: Control<TableForm>;
}

export function FilterCriteriaEditor({
  imageType,
  destIndex,
  control,
}: FilterCriteriaEditorProps) {

  const { fields, append, remove } = useFieldArray({
    control,
    name: `destinations.${destIndex}.filter_criteria.${imageType}` as const,
  });

  const addField = () => {
    append({ field: "", conditions: [] });
  };

  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between">
        <span className="text-xs font-medium text-muted-foreground uppercase tracking-wider">
          {imageType.replace("_", " ")}
        </span>
        <Button
          type="button"
          variant="outline"
          size="sm"
          onClick={addField}
        >
          <PlusIcon className="h-3.5 w-3.5 mr-1" />
          Add Field
        </Button>
      </div>

      {fields.length === 0 && (
        <p className="text-xs text-muted-foreground italic">
          No filters configured
        </p>
      )}

      {fields.map((field, fieldIndex) => (
        <div
          key={field.id}
          className="border border-border rounded-xl p-3 space-y-2 bg-card"
        >
          {/* Field Name + Remove */}
          <div className="flex items-center gap-2">
            <Controller
              name={`destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.field` as const}
              control={control}
              render={({ field: fieldProps }) => (
                <Input
                  {...fieldProps}
                  placeholder="Enter field name..."
                  className="h-8 text-xs w-[180px]"
                />
              )}
            />
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              onClick={() => remove(fieldIndex)}
              className="ml-auto"
            >
              <Trash2Icon className="h-3.5 w-3.5" />
            </Button>
          </div>

          {/* Conditions */}
          <Controller
            name={`destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.conditions` as const}
            control={control}
            render={({ field: conditionsProps }) => {
              const conditions = conditionsProps.value || [];
              const availableConditions = conditionOptions.filter(
                (opt) =>
                  !conditions.some((c) => c.type === opt.value),
              );

              return (
                <div className="space-y-2">
                  <Separator />

                  {conditions.map(
                    (condition, conditionIndex) => (
                      <div
                        key={conditionIndex}
                        className="flex items-center gap-2"
                      >
                        {/* Condition Type Badge */}
                        <span className="text-xs font-medium px-2 py-0.5 rounded bg-secondary text-secondary-foreground min-w-[80px] text-center">
                          {
                            conditionOptions.find(
                              (opt) => opt.value === condition.type,
                            )?.label
                          }
                        </span>

                        {/* Condition Value Input */}
                        {condition.type === "exists" ? (
                          <Controller
                            name={`destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.conditions.${conditionIndex}.value` as const}
                            control={control}
                            render={({ field }) => (
                              <Select
                                value={field.value || "true"}
                                onValueChange={field.onChange}
                              >
                                <SelectTrigger className="h-7 text-xs w-[100px]">
                                  <SelectValue />
                                </SelectTrigger>
                                <SelectContent>
                                  <SelectItem value="true">true</SelectItem>
                                  <SelectItem value="false">false</SelectItem>
                                </SelectContent>
                              </Select>
                            )}
                          />
                        ) : condition.type === "numeric" ? (
                          <>
                            <Controller
                              name={`destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.conditions.${conditionIndex}.numericOp` as const}
                              control={control}
                              render={({ field }) => (
                                <Select
                                  value={field.value || ">"}
                                  onValueChange={field.onChange}
                                >
                                  <SelectTrigger className="h-7 text-xs w-[70px]">
                                    <SelectValue />
                                  </SelectTrigger>
                                  <SelectContent>
                                    {numericOperators.map((op) => (
                                      <SelectItem key={op.value} value={op.value}>
                                        {op.label}
                                      </SelectItem>
                                    ))}
                                  </SelectContent>
                                </Select>
                              )}
                            />
                            <Controller
                              name={`destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.conditions.${conditionIndex}.value` as const}
                              control={control}
                              render={({ field }) => (
                                <Input
                                  {...field}
                                  type="number"
                                  placeholder="0"
                                  className="h-7 text-xs w-[100px]"
                                />
                              )}
                            />
                          </>
                        ) : (
                          <Controller
                            name={`destinations.${destIndex}.filter_criteria.${imageType}.${fieldIndex}.conditions.${conditionIndex}.value` as const}
                            control={control}
                            render={({ field }) => (
                              <Input
                                {...field}
                                placeholder={`Enter ${condition.type} value`}
                                className="h-7 text-xs flex-1"
                              />
                            )}
                          />
                        )}

                        {/* Remove Condition */}
                        <Button
                          type="button"
                          variant="ghost"
                          size="icon-sm"
                          onClick={() => {
                            const updated = conditions.filter(
                              (_, i) => i !== conditionIndex,
                            );
                            conditionsProps.onChange(updated);
                          }}
                        >
                          <Trash2Icon className="h-3 w-3" />
                        </Button>
                      </div>
                    ),
                  )}

                  {/* Add Condition Button */}
                  {availableConditions.length > 0 && (
                    <div className="flex flex-wrap gap-1">
                      {availableConditions.map((opt) => (
                        <Button
                          key={opt.value}
                          type="button"
                          variant="outline"
                          size="sm"
                          className="h-6 text-xs"
                          onClick={() => {
                            const newCondition = {
                              type: opt.value,
                              value: opt.value === "exists" ? "true" : "",
                              numericOp:
                                opt.value === "numeric" ? ">" : undefined,
                            };
                            conditionsProps.onChange([
                              ...conditions,
                              newCondition,
                            ]);
                          }}
                        >
                          + {opt.label}
                        </Button>
                      ))}
                    </div>
                  )}
                </div>
              );
            }}
          />
        </div>
      ))}
    </div>
  );
}
