import { sessionCoordinator } from "@/core/session/browser";
import { StaleSessionError } from "@/core/session/session";
import { momentsFeedKey, updateCachedMoment } from "@/core/momentsCache";
import { useInfiniteQuery, useMutation, useQueryClient } from "@tanstack/react-query";

import { getChatUrl } from "@/config";
import { WorkMoments, WorkMomentsResponse } from "@/types/moment";
import createAxiosInstance from "@/utils/request";

const request = createAxiosInstance(getChatUrl, "chat");
const currentNamespace = () =>
  sessionCoordinator.current ? sessionCoordinator.namespace : "anonymous";
const assertGeneration = (generation: number) => {
  if (generation !== sessionCoordinator.generation) throw new StaleSessionError();
};
const useMomentMutation = <T>(path: string, invalidateFeed = false) => {
  const client = useQueryClient();
  const generation = sessionCoordinator.generation;
  const namespace = currentNamespace();
  return useMutation({
    mutationFn: async (params: T) => {
      assertGeneration(generation);
      const response = await request.post<{ workMoment?: WorkMoments }>(path, params);
      assertGeneration(generation);
      return response;
    },
    onSuccess: (response) => {
      if (generation !== sessionCoordinator.generation) return;
      if (response.data.workMoment)
        updateCachedMoment(client, namespace, response.data.workMoment);
      if (invalidateFeed)
        void client.invalidateQueries({ queryKey: ["moments", namespace, "feed"] });
    },
  });
};

// publish moments
export const usePublishMoments = (_queryKey?: string) =>
  useMomentMutation<API.Moments.PublishMomentsParams>("/office/work_moment/add", true);

export const fetchUserMoments = async (
  { pageParam = 1 },
  userID?: string,
): Promise<WorkMoments[]> => {
  const url = `/office/work_moment/find/${!userID ? "recv" : "send"}`;
  const { data } = await request.post<WorkMomentsResponse>(url, {
    userID,
    pagination: {
      pageNumber: pageParam,
      showNumber: 20,
    },
  });
  return data.workMoments;
};
export const useUserMoments = (enabled: boolean, queryKey?: string) => {
  const generation = sessionCoordinator.generation;
  return useInfiniteQuery<WorkMoments[]>({
    queryKey: momentsFeedKey(currentNamespace(), queryKey),
    queryFn: (context) => {
      assertGeneration(generation);
      return fetchUserMoments({ pageParam: Number(context.pageParam) }, queryKey);
    },
    initialPageParam: 1,

    getNextPageParam: (lastPage, pages) =>
      lastPage?.length < 20 ? undefined : pages.length + 1,
    enabled,
  });
};

export const getMomentsByID = (workMomentID: string) =>
  request.post<{ workMoment: WorkMoments }>("/office/work_moment/get", {
    workMomentID,
  });

export const useDeleteMoments = (_queryKey?: string) => {
  const mutation = useMomentMutation<{ workMomentID: string }>(
    "/office/work_moment/del",
    true,
  );
  return {
    ...mutation,
    mutateAsync: (workMomentID: string) => mutation.mutateAsync({ workMomentID }),
  };
};

export const useCreateComment = () =>
  useMomentMutation<API.Moments.CreateCommentParams>("/office/work_moment/comment/add");
export const useDeleteComment = () =>
  useMomentMutation<API.Moments.DeleteCommentParams>("/office/work_moment/comment/del");
export const useLikeMoments = () =>
  useMomentMutation<{ workMomentID: string; like: boolean }>(
    "/office/work_moment/like",
  );

// Querying for no readings
export const getMomentsUnreadCount = () =>
  request.post("/office/work_moment/unread/count", {});

// Query message list
export const useLogs = () => {
  const generation = sessionCoordinator.generation;
  return useInfiniteQuery<{ data: WorkMomentsResponse }>({
    queryKey: ["moments", currentNamespace(), "logs"],
    queryFn: ({ pageParam = 1 }) => {
      assertGeneration(generation);
      return request.post("/office/work_moment/logs", {
        pagination: {
          pageNumber: pageParam as number,
          showNumber: 20,
        },
      });
    },
    initialPageParam: 1,

    getNextPageParam: (lastPage, pages) => {
      if ((lastPage.data.workMoments?.length ?? 0) < 20) return null;
      return pages.length + 1;
    },
  });
};

// Query message list
export enum MomentsClearType {
  Count = 1,
  List = 2,
  All = 3,
}
export const useClearUnreadMoments = () => {
  const mutation = useMomentMutation<{ type: MomentsClearType }>(
    "/office/work_moment/unread/clear",
  );
  return {
    ...mutation,
    mutateAsync: (type: MomentsClearType) => mutation.mutateAsync({ type }),
  };
};
