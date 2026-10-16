import { MessageItem } from "open-im-sdk-wasm/lib/types/entity";
import { Component, ErrorInfo, ReactNode } from "react";

import CatchMessageRender from "./CatchMsgRenderer";

type MessageItemErrorBoundaryProps = {
  children: ReactNode;
  message: MessageItem;
};

type MessageItemErrorBoundaryState = {
  hasError: boolean;
};

class MessageItemErrorBoundary extends Component<
  MessageItemErrorBoundaryProps,
  MessageItemErrorBoundaryState
> {
  constructor(props: MessageItemErrorBoundaryProps) {
    super(props);
    this.state = { hasError: false };
  }

  componentDidCatch(error: Error, errorInfo: ErrorInfo) {
    // Do not retain or log original message bodies/attachment URLs on render errors.

    this.setState({ hasError: true });
  }

  render() {
    if (this.state.hasError) {
      return <CatchMessageRender />;
    }

    return this.props.children;
  }
}

export default MessageItemErrorBoundary;
