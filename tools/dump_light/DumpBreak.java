import java.io.FileOutputStream;
import java.io.OutputStream;

import net.minecraft.SharedConstants;
import net.minecraft.core.registries.BuiltInRegistries;
import net.minecraft.server.Bootstrap;
import net.minecraft.world.level.block.Block;
import net.minecraft.world.level.block.state.BlockState;

/**
 * Writes what a server needs to time a block breaking itself:
 *
 *   hardness.bin  two bytes per state, big-endian, hardness * 100, or -1 for
 *                 unbreakable (vanilla says -1)
 *   stateTools.bin  one byte per state, bit 6 set when the state requires the
 *                 correct tool to drop anything
 *
 * Both are computed by the block, so neither is in the block report.
 *
 * The tool *class* per state comes from the mineable/* block tags, which the
 * server already generates, and the tier speeds from a small written-out table —
 * doing it through items would mean asking for their Tool component, and item
 * components are not bound in this harness ("Components not bound yet", even
 * after Bootstrap.bootStrap()).
 */
public class DumpBreak {
   public static void main(final String[] args) throws Exception {
      SharedConstants.tryDetectVersion();
      Bootstrap.bootStrap();

      int states = Block.BLOCK_STATE_REGISTRY.size();
      byte[] hardness = new byte[states * 2];
      byte[] stateTools = new byte[states];
      for (Block block : BuiltInRegistries.BLOCK) {
         for (BlockState state : block.getStateDefinition().getPossibleStates()) {
            int id = Block.getId(state);
            float h = state.getDestroySpeed(null, null); // the accessor ignores both
            long v = Math.max(-32768, Math.min(32767, Math.round(h * 100f)));
            hardness[id * 2] = (byte) (v >> 8);
            hardness[id * 2 + 1] = (byte) v;
            if (state.requiresCorrectToolForDrops()) {
               stateTools[id] = (byte) 0x40;
            }
         }
      }
      write(args[0], hardness);
      write(args[1], stateTools);
      System.out.println("wrote " + states + " hardness and tool requirement entries");
   }

   private static void write(final String path, final byte[] bytes) throws Exception {
      try (OutputStream os = new FileOutputStream(path)) {
         os.write(bytes);
      }
   }
}
