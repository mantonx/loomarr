import { Text as TamaguiText, useTheme, View } from "@tamagui/core";
import type { ComponentProps, ComponentRef, ReactNode } from "react";
import { forwardRef, useState } from "react";
import { Pressable, TextInput } from "react-native";

import { Icon } from "../icon";
import { type IconName, icons } from "../icons";
import { Surface, Text } from "../primitives";
import { type Density, semanticRadius, semanticSpace, semanticTargets, typography } from "../tokens";

type ActionProps = Omit<ComponentProps<typeof Pressable>, "children" | "style"> & {
  children: ReactNode;
  density?: Density;
  icon?: IconName;
  selected?: boolean;
  style?: ComponentProps<typeof Pressable>["style"];
  tone?: "danger" | "primary" | "secondary";
};

const Action = forwardRef<ComponentRef<typeof Pressable>, ActionProps>(
  (
    {
      accessibilityState,
      children,
      density = "pointer",
      disabled = false,
      icon,
      onBlur,
      onFocus,
      selected = false,
      style,
      tabIndex,
      tone = "primary",
      ...props
    },
    ref,
  ) => {
    const theme = useTheme();
    const [focused, setFocused] = useState(false);
    const isDisabled = disabled === true;
    const role = props.accessibilityRole ?? "button";
    const tv = density === "tv";
    const backgroundColor =
      tone === "danger"
        ? theme.stateDanger.val
        : tone === "primary"
          ? theme.actionPrimary.val
          : selected
            ? theme.surfaceFocus.val
            : theme.surfaceElevated.val;
    const borderColor =
      focused || selected
        ? theme.actionFocus.val
        : tone === "danger"
          ? theme.stateDanger.val
          : tone === "primary"
            ? theme.actionPrimary.val
            : theme.borderControl.val;
    return (
      <Pressable
        {...props}
        accessibilityState={{ ...accessibilityState, disabled: isDisabled, selected }}
        accessibilityRole={role}
        aria-disabled={isDisabled || undefined}
        aria-pressed={role === "button" && selected ? true : undefined}
        aria-selected={role === "tab" ? selected : undefined}
        disabled={isDisabled}
        onBlur={(event) => {
          setFocused(false);
          onBlur?.(event);
        }}
        onFocus={(event) => {
          setFocused(true);
          onFocus?.(event);
        }}
        ref={ref}
        tabIndex={isDisabled ? -1 : tabIndex}
        style={(state) => [
          {
            alignItems: "center",
            backgroundColor,
            borderColor,
            borderRadius: tv ? 8 : semanticRadius.control,
            borderStyle: "solid",
            borderWidth: focused ? (tv ? 3 : 4) : tv ? 1 : 2,
            justifyContent: "center",
            minHeight: tv ? 0 : semanticTargets[density],
            opacity: isDisabled ? 0.55 : state.pressed ? 0.82 : 1,
            paddingHorizontal: tv ? 24 : semanticSpace.control,
            paddingVertical: tv ? 8 : 0,
            transform: [
              { scale: tv ? (state.pressed ? 0.98 : 1) : focused ? 1.025 : state.pressed ? 0.98 : 1 },
            ],
          },
          typeof style === "function" ? style(state) : style,
        ]}
      >
        {icon ? (
          <View alignItems="center" flexDirection="row" gap="$inline">
            <Icon
              decorative
              glyph={icons[icon]}
              size={density === "tv" ? "touch" : "default"}
              tone={tone === "secondary" && !selected ? "secondary" : "inverse"}
            />
            <TamaguiText
              color={tone === "secondary" ? "$contentPrimary" : "$contentInverse"}
              fontFamily="$body"
              fontSize={tv ? typography.tv.data.size : typography[density].label.size}
              fontWeight={tv ? "400" : "700"}
            >
              {children}
            </TamaguiText>
          </View>
        ) : (
          <TamaguiText
            color={tone === "secondary" ? "$contentPrimary" : "$contentInverse"}
            fontFamily="$body"
            fontSize={tv ? typography.tv.data.size : typography[density].label.size}
            fontWeight={tv ? "400" : "700"}
          >
            {children}
          </TamaguiText>
        )}
      </Pressable>
    );
  },
);

Action.displayName = "Action";

type FieldProps = Omit<ComponentProps<typeof TextInput>, "editable"> & {
  density?: Density;
  description?: string;
  disabled?: boolean;
  error?: string;
  invalid?: boolean;
  label?: string;
};

const Field = ({
  accessibilityLabel,
  density = "pointer",
  description,
  disabled = false,
  error,
  invalid = false,
  label,
  onBlur,
  onFocus,
  style,
  ...props
}: FieldProps) => {
  const theme = useTheme();
  const [focused, setFocused] = useState(false);
  const hasError = invalid || Boolean(error);
  const borderColor = hasError
    ? theme.stateDanger.val
    : focused
      ? theme.actionFocus.val
      : theme.borderControl.val;

  return (
    <View gap="$inline" width="100%">
      {label ? (
        <Text density={density} textRole="label">
          {label}
        </Text>
      ) : null}
      <TextInput
        {...props}
        accessibilityLabel={accessibilityLabel ?? label}
        accessibilityState={{ disabled }}
        aria-disabled={disabled || undefined}
        aria-invalid={hasError || undefined}
        editable={!disabled}
        onBlur={(event) => {
          setFocused(false);
          onBlur?.(event);
        }}
        onFocus={(event) => {
          setFocused(true);
          onFocus?.(event);
        }}
        placeholderTextColor={theme.contentMuted.val}
        style={[
          {
            backgroundColor: disabled ? theme.surfaceRaised.val : theme.surfaceCanvas.val,
            borderColor,
            borderRadius: semanticRadius.control,
            borderWidth: focused ? 3 : 2,
            color: theme.contentPrimary.val,
            fontFamily: typography.family.data.native,
            fontSize: typography[density].body.size,
            minHeight: semanticTargets[density],
            opacity: disabled ? 0.62 : 1,
            paddingHorizontal: semanticSpace.control,
          },
          style,
        ]}
      />
      {error ? (
        <Text aria-live="polite" density={density} textRole="metadata" tone="danger">
          {error}
        </Text>
      ) : description ? (
        <Text density={density} textRole="metadata">
          {description}
        </Text>
      ) : null}
    </View>
  );
};

type ToggleProps = {
  checked: boolean;
  density?: Density;
  description?: string;
  disabled?: boolean;
  kind?: "checkbox" | "switch";
  label: string;
  onCheckedChange: (checked: boolean) => void;
};

const Toggle = ({
  checked,
  density = "pointer",
  description,
  disabled = false,
  kind = "checkbox",
  label,
  onCheckedChange,
}: ToggleProps) => {
  const switchWidth = density === "tv" ? 72 : density === "touch" ? 56 : 48;
  const controlSize = density === "tv" ? 36 : density === "touch" ? 28 : 24;
  return (
    <Pressable
      accessibilityLabel={label}
      accessibilityRole={kind}
      accessibilityState={{ checked, disabled }}
      aria-checked={checked}
      aria-disabled={disabled || undefined}
      disabled={disabled}
      onPress={() => onCheckedChange(!checked)}
      style={{
        alignItems: "center",
        flexDirection: "row",
        gap: semanticSpace.control,
        minHeight: semanticTargets[density],
        opacity: disabled ? 0.55 : 1,
      }}
    >
      <Surface
        alignItems={kind === "switch" ? (checked ? "flex-end" : "flex-start") : "center"}
        backgroundColor={checked ? "$actionPrimary" : "$surfaceCanvas"}
        borderColor={checked ? "$actionPrimary" : "$borderControl"}
        borderRadius={kind === "switch" ? "$round" : "$control"}
        height={controlSize}
        justifyContent="center"
        padding={kind === "switch" ? 3 : 0}
        width={kind === "switch" ? switchWidth : controlSize}
      >
        {kind === "switch" ? (
          <View
            backgroundColor={checked ? "$contentInverse" : "$contentSecondary"}
            borderRadius="$round"
            height={controlSize - 8}
            width={controlSize - 8}
          />
        ) : checked ? (
          <Icon
            decorative
            glyph={icons.success}
            size={density === "tv" ? "control" : "compact"}
            tone="inverse"
          />
        ) : null}
      </Surface>
      <View flex={1} gap={2}>
        <Text density={density} textRole="label">
          {label}
        </Text>
        {description ? (
          <Text density={density} textRole="metadata">
            {description}
          </Text>
        ) : null}
      </View>
    </Pressable>
  );
};

type ChoiceOption<Value extends string> = {
  description?: string;
  disabled?: boolean;
  label: string;
  value: Value;
};

type ChoiceGroupProps<Value extends string> = {
  density?: Density;
  label: string;
  onValueChange: (value: Value) => void;
  options: readonly ChoiceOption<Value>[];
  value: Value;
};

const ChoiceGroup = <Value extends string>({
  density = "pointer",
  label,
  onValueChange,
  options,
  value,
}: ChoiceGroupProps<Value>) => (
  <View aria-label={label} role="radiogroup" gap="$inline">
    <Text density={density} textRole="label">
      {label}
    </Text>
    {options.map((option) => {
      const checked = option.value === value;
      return (
        <Pressable
          accessibilityLabel={option.label}
          accessibilityRole="radio"
          accessibilityState={{ checked, disabled: option.disabled }}
          aria-checked={checked}
          aria-disabled={option.disabled || undefined}
          disabled={option.disabled}
          key={option.value}
          onPress={() => onValueChange(option.value)}
          style={{
            alignItems: "center",
            flexDirection: "row",
            gap: semanticSpace.control,
            minHeight: semanticTargets[density],
            opacity: option.disabled ? 0.55 : 1,
          }}
        >
          <Surface
            alignItems="center"
            backgroundColor={checked ? "$actionPrimary" : "$surfaceCanvas"}
            borderColor={checked ? "$actionPrimary" : "$borderControl"}
            borderRadius="$round"
            height={density === "tv" ? 36 : 24}
            justifyContent="center"
            width={density === "tv" ? 36 : 24}
          >
            {checked ? (
              <View backgroundColor="$contentInverse" borderRadius="$round" height="45%" width="45%" />
            ) : null}
          </Surface>
          <View flex={1} gap={2}>
            <Text density={density} textRole="label">
              {option.label}
            </Text>
            {option.description ? (
              <Text density={density} textRole="metadata">
                {option.description}
              </Text>
            ) : null}
          </View>
        </Pressable>
      );
    })}
  </View>
);

export type { ActionProps, ChoiceGroupProps, ChoiceOption, FieldProps, ToggleProps };
export { Action, ChoiceGroup, Field, Toggle };
