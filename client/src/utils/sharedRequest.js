// Fork addition: one request for everybody who asks at the same moment, and
// its answer kept for a short while. Upstream asks the same question many
// times while a page loads (GET /api/me once per entry of the route table);
// this makes it one request. Nothing here knows about the API: `load` sends
// the request, so the module can be exercised on its own.
//
//   const shared = createSharedRequest(load, { ttl: 5000, keep: (answer) => … });
//   shared.get()         the request in flight, the kept answer, or a new request
//   shared.invalidate()  forget the kept answer, and do not keep the answer of
//                        a request that is still in flight
export const createSharedRequest = (load, options = {}) => {
  const ttl = options.ttl === undefined ? 5000 : options.ttl;
  const now = options.now || (() => Date.now());
  // which answers may be kept: by default every answer that is not a failure
  const keep = options.keep || (() => true);

  let pending = null; // the request in flight
  let kept = null; // { answer, at }
  let generation = 0; // raised by invalidate()

  const get = () => {
    if (pending) {
      return pending;
    }
    if (kept && now() - kept.at < ttl) {
      return Promise.resolve(kept.answer);
    }
    kept = null;
    const mine = generation;
    const request = Promise.resolve()
      .then(() => load())
      .then(
        (answer) => {
          if (pending === request) pending = null;
          let mayKeep = false;
          try {
            mayKeep = !!keep(answer);
          } catch (e) {
            mayKeep = false;
          }
          if (mine === generation && mayKeep) {
            kept = { answer, at: now() };
          }
          return answer;
        },
        (error) => {
          // a failure is never kept: the next caller asks again
          if (pending === request) pending = null;
          throw error;
        },
      );
    pending = request;
    return request;
  };

  const invalidate = () => {
    generation += 1;
    kept = null;
    pending = null;
  };

  return { get, invalidate };
};
