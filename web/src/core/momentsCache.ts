import type { InfiniteData, QueryClient } from "@tanstack/react-query";
import type { WorkMoments } from "@/types/moment";

export const momentsFeedKey = (namespace: string, userID?: string) =>
  ["moments", namespace, "feed", userID ?? null] as const;
export function updateCachedMoment(
  client: QueryClient,
  namespace: string,
  moment: WorkMoments,
): void {
  client.setQueriesData<InfiniteData<WorkMoments[]>>(
    { queryKey: ["moments", namespace, "feed"] },
    (previous) =>
      previous
        ? {
            ...previous,
            pages: previous.pages.map((page) =>
              page.map((item) =>
                item.workMomentID === moment.workMomentID ? moment : item,
              ),
            ),
          }
        : previous,
  );
}
