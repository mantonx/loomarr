import QRCodeEncoder from "qrcode";

const matrixPath = (value: string, errorCorrectionLevel: "H" | "L" = "H") => {
  // Branded codes need H because the protected centre mark obscures modules. Plain codes use the
  // standard L treatment used by the original TV client, leaving a simpler matrix at viewing range.
  const matrix = QRCodeEncoder.create(value, { errorCorrectionLevel }).modules;
  const commands: string[] = [];

  for (let row = 0; row < matrix.size; row += 1) {
    let column = 0;
    while (column < matrix.size) {
      while (column < matrix.size && matrix.get(row, column) === 0) column += 1;
      const start = column;
      while (column < matrix.size && matrix.get(row, column) !== 0) column += 1;
      if (column > start) commands.push(`M${start + 4} ${row + 4}h${column - start}v1H${start + 4}z`);
    }
  }

  return { commands: commands.join(""), dimension: matrix.size + 8 };
};

export { matrixPath };
