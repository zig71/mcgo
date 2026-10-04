import java.io.FileOutputStream;
import java.io.OutputStream;

import net.minecraft.SharedConstants;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.state.BlockState;

/**
 * Writes one byte per block state to the files named in argv: first the light
 * dampening, then the light emission.
 *
 * Both are computed by the block itself — dampening from BlockStateBase's cached
 * lightDampening (which depends on per-state render and collision shapes) and
 * emission from getLightEmission — so neither can be read out of the data
 * generator's block report, and a hand-written table gets them wrong. Running
 * the real code is the only way to get them.
 */
public class DumpLight {
   public static void main(final String[] args) throws Exception {
      SharedConstants.tryDetectVersion();
      Bootstrap.bootStrap();

      int states = Block.BLOCK_STATE_REGISTRY.size();
      byte[] dampening = new byte[states];
      byte[] emission = new byte[states];
      for (Block block : BuiltInRegistries.BLOCK) {
         for (BlockState state : block.getStateDefinition().getPossibleStates()) {
            int id = Block.getId(state);
            if (id < 0 || id >= states) {
               throw new IllegalStateException("state id " + id + " outside 0.." + states);
            }
            dampening[id] = (byte) state.getLightDampening();
            emission[id] = (byte) state.getLightEmission();
         }
      }
      write(args[0], dampening);
      System.out.println("wrote " + states + " dampening values to " + args[0]);
      if (args.length > 1) {
         write(args[1], emission);
         System.out.println("wrote " + states + " emission values to " + args[1]);
      }
   }

   private static void write(final String path, final byte[] bytes) throws Exception {
      try (OutputStream os = new FileOutputStream(path)) {
         os.write(bytes);
      }
   }
}
