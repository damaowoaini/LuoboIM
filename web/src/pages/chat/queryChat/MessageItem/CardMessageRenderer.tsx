import { FC } from "react";
import ReceivedMessageCard from "@/components/ReceivedMessageCard";
import type { IMessageItemProps } from ".";

const CardMessageRenderer: FC<IMessageItemProps> = ({ message, conversationID }) =>
  <ReceivedMessageCard message={message} conversationID={conversationID} />;
export default CardMessageRenderer;
