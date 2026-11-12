export type Sub2ApiBootstrapCoordinator<TResult> = {
  readonly started: boolean;
  run: () => Promise<TResult>;
};

export function createSub2ApiBootstrapCoordinator<TResult>(
  task: () => Promise<TResult>,
): Sub2ApiBootstrapCoordinator<TResult> {
  let promise: Promise<TResult> | null = null;
  return {
    get started() {
      return promise !== null;
    },
    run() {
      promise ??= Promise.resolve().then(task);
      return promise;
    },
  };
}
