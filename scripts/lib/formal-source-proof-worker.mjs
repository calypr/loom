import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { join } from 'node:path';
import { sourceProofs, checkSourceProof, replayPredicateWitness } from './formal-source-proofs.mjs';
if (process.argv[2] === '--witness') {
    const witness = JSON.parse(readFileSync(process.argv[3], 'utf8'));
    const source = process.argv[4] ? readFileSync(process.argv[4], 'utf8') : undefined;
    const result = await replayPredicateWitness(witness, source);
    console.log(JSON.stringify(result));
    process.exitCode = result.status === 'reproduced' ? 1 : 0;
}
else {
    const [index, graphFile, output] = process.argv.slice(2);
    mkdirSync(output, { recursive: true });
    try {
        const proof = sourceProofs[Number(index)];
        if (!proof)
            throw new Error('Unknown source-proof index');
        const graph = graphFile === '-' ? undefined : JSON.parse(readFileSync(graphFile, 'utf8'));
        await checkSourceProof(proof, graph, output);
    }
    catch (error) {
        writeFileSync(join(output, 'proof-error.json'), JSON.stringify({ message: error.message, code: error.code, stack: error.stack }, null, 2));
        console.error(error.stack);
        process.exitCode = 1;
    }
}
