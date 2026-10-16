import type { ThemeConfig } from "antd";

// Web adaptation of app/lib/design/tokens.dart. One source feeds AntD and CSS,
// including body portals; this does not install or change session state.
export const mobileDesign = {
  primary: "#2563eb",
  background: "#f2f4f7",
  surface: "#ffffff",
  text: "#171a1f",
  secondary: "#5b6270",
  divider: "#e3e7ed",
  error: "#b42318",
  primarySoft: "#dfeaff", // Mobile contact/conversation avatar surface.
  bubbleSent: "#dce8ff", // Mobile ChatPage's normal outgoing bubble.
  radiusControl: 12,
  touchTarget: 44,
  primaryButtonHeight: 52,
  fontFamily: '-apple-system, BlinkMacSystemFont, "Segoe UI", "Noto Sans SC", "PingFang SC", "Microsoft YaHei", sans-serif',
} as const;

const cssVariables = {
  "--luobo-primary": mobileDesign.primary,
  "--luobo-background": mobileDesign.background,
  "--luobo-surface": mobileDesign.surface,
  "--luobo-text": mobileDesign.text,
  "--luobo-secondary": mobileDesign.secondary,
  "--luobo-divider": mobileDesign.divider,
  "--luobo-error": mobileDesign.error,
  "--luobo-primary-soft": mobileDesign.primarySoft,
  "--luobo-bubble-sent": mobileDesign.bubbleSent,
  "--luobo-radius-control": `${mobileDesign.radiusControl}px`,
  "--luobo-touch-target": `${mobileDesign.touchTarget}px`,
  "--luobo-primary-button-height": `${mobileDesign.primaryButtonHeight}px`,
  "--luobo-font-family": mobileDesign.fontFamily,
  "--luobo-sidebar-width": "300px",
  "--luobo-scrollbar": "#b6bdc9",
};

export const mobileTokenStyles = `:root{${Object.entries(cssVariables)
  .map(([name, value]) => `${name}:${value}`)
  .join(";")}}`;

export const luoboAntdTheme: ThemeConfig = {
  token: {
    colorPrimary: mobileDesign.primary,
    colorBgLayout: mobileDesign.background,
    colorBgContainer: mobileDesign.surface,
    colorBgElevated: mobileDesign.surface,
    colorText: mobileDesign.text,
    colorTextSecondary: mobileDesign.secondary,
    colorBorder: mobileDesign.divider,
    colorBorderSecondary: mobileDesign.divider,
    colorSplit: mobileDesign.divider,
    colorError: mobileDesign.error,
    borderRadius: mobileDesign.radiusControl,
    borderRadiusLG: mobileDesign.radiusControl,
    controlHeight: mobileDesign.touchTarget,
    controlHeightLG: mobileDesign.primaryButtonHeight,
    controlHeightSM: mobileDesign.touchTarget,
    fontFamily: mobileDesign.fontFamily,
    fontSize: 14,
    fontSizeLG: 16,
  },
  components: {
    Button: { fontWeight: 600, primaryShadow: "none" },
    Input: { paddingInline: 16 },
    Layout: {
      bodyBg: mobileDesign.background,
      siderBg: mobileDesign.surface,
      headerBg: mobileDesign.surface,
    },
    Menu: { itemSelectedBg: mobileDesign.primarySoft },
    Tabs: { itemSelectedColor: mobileDesign.primary },
  },
};
