const compareText = (left, right) => left < right ? -1 : left > right ? 1 : 0;

const compareTuples = (left, right) =>
  compareText(left[0], right[0]) || left[1] - right[1] || compareText(left[2], right[2]);

export function strictSubsetFilterOracle(selected) {
  if (!Array.isArray(selected)) return null;

  const allTuples = selected.flatMap(({ id, componentValues }) =>
    componentValues.map(({ ordinal, value }) => [id, ordinal, value]));
  if (allTuples.length === 0) return null;
  allTuples.sort(compareTuples);

  const candidateValues = [...new Set(allTuples.map(([, , value]) => value))].sort(compareText);
  const predicateValue = candidateValues.find(value => {
    const matchCount = allTuples.reduce((count, tuple) => count + Number(tuple[2] === value), 0);
    return matchCount > 0 && matchCount < allTuples.length;
  });
  if (predicateValue === undefined) return null;

  return {
    predicateValue,
    allTuples,
    matchingTuples: allTuples.filter(([, , value]) => value === predicateValue),
  };
}
