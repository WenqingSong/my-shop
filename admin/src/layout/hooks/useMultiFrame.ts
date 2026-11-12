const MAP = new Map();

export const useMultiFrame = () => {
  function setMap(path: string, Comp: any) {
    MAP.set(path, Comp);
  }

  function getMap(path?: string) {
    if (path) {
      return MAP.get(path);
    }
    return [...MAP.entries()];
  }

  function delMap(path: string) {
    MAP.delete(path);
  }

  return {
    setMap,
    getMap,
    delMap,
    MAP
  };
};
