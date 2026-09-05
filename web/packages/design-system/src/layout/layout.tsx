import { useTheme, View } from "@tamagui/core";
import type { ComponentProps, ComponentRef, ReactNode } from "react";
import { forwardRef, useId, useState } from "react";
import { Pressable, ScrollView, useWindowDimensions } from "react-native";

import { Text } from "../primitives";
import { type Density, semanticSpace, semanticTargets } from "../tokens";

type AdaptiveDirection = "column" | "row";

const adaptiveBreakpoints: Record<Density, number> = {
  pointer: 900,
  touch: 720,
  tv: 1280,
};

const resolveAdaptiveDirection = (
  width: number,
  density: Density,
  breakpoint = adaptiveBreakpoints[density],
): AdaptiveDirection => (width >= breakpoint ? "row" : "column");

type AdaptiveSplitProps = Omit<ComponentProps<typeof View>, "children" | "flexDirection"> & {
  breakpoint?: number;
  density?: Density;
  primary: ReactNode;
  secondary: ReactNode;
  secondaryWidth?: number | string;
};

/**
 * A two-region layout that owns the wide/narrow transition. Product modules supply regions,
 * not viewport checks, so web and native hosts share the same composition rule.
 */
const AdaptiveSplit = ({
  breakpoint,
  density = "pointer",
  gap = "$section",
  primary,
  secondary,
  secondaryWidth,
  ...props
}: AdaptiveSplitProps) => {
  const { width } = useWindowDimensions();
  const direction = resolveAdaptiveDirection(width, density, breakpoint);
  const isWide = direction === "row";
  const resolvedSecondaryWidth = secondaryWidth ?? (density === "tv" ? 520 : 360);

  return (
    <View {...props} alignItems="stretch" flexDirection={direction} gap={gap} width="100%">
      <View flex={isWide ? 1 : undefined} minWidth={0} width={isWide ? undefined : "100%"}>
        {primary}
      </View>
      <View flexShrink={0} width={isWide ? resolvedSecondaryWidth : "100%"}>
        {secondary}
      </View>
    </View>
  );
};

type ScrollFrameProps = Omit<
  ComponentProps<typeof ScrollView>,
  "children" | "contentContainerStyle" | "horizontal"
> & {
  children: ReactNode;
  contentContainerStyle?: ComponentProps<typeof ScrollView>["contentContainerStyle"];
  density?: Density;
};

/** A vertical application scroller with consistent content rhythm and keyboard behavior. */
const ScrollFrame = forwardRef<ComponentRef<typeof ScrollView>, ScrollFrameProps>(
  (
    {
      children,
      contentContainerStyle,
      density = "pointer",
      keyboardShouldPersistTaps = "handled",
      style,
      ...props
    },
    ref,
  ) => (
    <ScrollView
      {...props}
      contentContainerStyle={[
        { flexGrow: 1, gap: density === "tv" ? semanticSpace.section : semanticSpace.control },
        contentContainerStyle,
      ]}
      keyboardShouldPersistTaps={keyboardShouldPersistTaps}
      ref={ref}
      style={[{ flex: 1 }, style]}
    >
      {children}
    </ScrollView>
  ),
);

ScrollFrame.displayName = "ScrollFrame";

type DisclosureProps = Omit<ComponentProps<typeof View>, "children"> & {
  children: ReactNode;
  density?: Density;
  description?: string;
  disabled?: boolean;
  expanded: boolean;
  label: string;
  onExpandedChange: (expanded: boolean) => void;
};

/**
 * Shared progressive disclosure for optional detail. It owns expanded semantics, focus treatment,
 * and the target size while allowing each host to use its native Pressable mechanics.
 */
const Disclosure = ({
  children,
  density = "pointer",
  description,
  disabled = false,
  expanded,
  label,
  onExpandedChange,
  ...props
}: DisclosureProps) => {
  const generatedId = useId();
  const contentId = `loomarr-disclosure-${generatedId.replaceAll(":", "")}`;
  const [focused, setFocused] = useState(false);
  const theme = useTheme();

  return (
    <View
      {...props}
      backgroundColor="$surfaceRaised"
      borderColor={focused ? "$actionFocus" : "$borderDecorative"}
      borderRadius="$card"
      borderWidth={focused ? 3 : 1}
      overflow="hidden"
      width="100%"
    >
      <Pressable
        accessibilityRole="button"
        accessibilityState={{ disabled, expanded }}
        aria-controls={contentId}
        aria-disabled={disabled || undefined}
        aria-expanded={expanded}
        disabled={disabled}
        onBlur={() => setFocused(false)}
        onFocus={() => setFocused(true)}
        onPress={() => onExpandedChange(!expanded)}
        style={({ pressed }) => ({
          alignItems: "center",
          backgroundColor: focused ? theme.surfaceFocus.val : theme.surfaceRaised.val,
          flexDirection: "row",
          gap: semanticSpace.control,
          minHeight: semanticTargets[density],
          opacity: disabled ? 0.55 : pressed ? 0.82 : 1,
          paddingHorizontal: semanticSpace.control,
          paddingVertical: semanticSpace.inline,
        })}
      >
        <View alignItems="flex-start" flex={1} gap={2}>
          <Text density={density} textAlign="left" textRole="label" tone="primary">
            {label}
          </Text>
          {description ? (
            <Text density={density} textAlign="left" textRole="metadata">
              {description}
            </Text>
          ) : null}
        </View>
        <Text density={density} textRole="metadata" tone={focused ? "primary" : "secondary"}>
          {expanded ? "Hide" : "Show"}
        </Text>
      </Pressable>
      {expanded ? (
        <View
          borderTopColor="$borderDecorative"
          borderTopWidth={1}
          gap="$control"
          id={contentId}
          nativeID={contentId}
          padding="$control"
        >
          {children}
        </View>
      ) : null}
    </View>
  );
};

export type { AdaptiveDirection, AdaptiveSplitProps, DisclosureProps, ScrollFrameProps };
export { AdaptiveSplit, Disclosure, resolveAdaptiveDirection, ScrollFrame };
