package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

const (
	rtIcon      = 3
	rtGroupIcon = 14
	rtManifest  = 24

	imageFileMachineAMD64 = 0x8664
	imageRelAMD64Addr32NB = 0x0003
	imageScnCntInitData   = 0x00000040
	imageScnMemRead       = 0x40000000
	imageSymClassStatic   = 3
)

const appManifest = `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<assembly xmlns="urn:schemas-microsoft-com:asm.v1" manifestVersion="1.0">
  <assemblyIdentity version="1.0.0.0" processorArchitecture="amd64" name="ShareNet.Desktop" type="win32"/>
  <description>ShareNet 2-in-1</description>
  <trustInfo xmlns="urn:schemas-microsoft-com:asm.v3">
    <security>
      <requestedPrivileges>
        <requestedExecutionLevel level="asInvoker" uiAccess="false"/>
      </requestedPrivileges>
    </security>
  </trustInfo>
  <compatibility xmlns="urn:schemas-microsoft-com:compatibility.v1">
    <application>
      <supportedOS Id="{8e0f7a12-bfb3-4fe8-b9a5-48fd50a15a9a}"/>
    </application>
  </compatibility>
  <application xmlns="urn:schemas-microsoft-com:asm.v3">
    <windowsSettings>
      <dpiAwareness xmlns="http://schemas.microsoft.com/SMI/2016/WindowsSettings">PerMonitorV2, PerMonitor</dpiAwareness>
    </windowsSettings>
  </application>
</assembly>`

type resItem struct {
	typ  uint32
	id   uint32
	lang uint32
	data []byte
}

type iconDirEntry struct {
	Width       uint8
	Height      uint8
	ColorCount  uint8
	Reserved    uint8
	Planes      uint16
	BitCount    uint16
	BytesInRes  uint32
	ImageOffset uint32
}

type grpIconDirEntry struct {
	Width      uint8
	Height     uint8
	ColorCount uint8
	Reserved   uint8
	Planes     uint16
	BitCount   uint16
	BytesInRes uint32
	ID         uint16
}

func parseICO(raw []byte) (items []resItem, err error) {
	if len(raw) < 6 {
		return nil, fmt.Errorf("ico too small")
	}
	resType := binary.LittleEndian.Uint16(raw[2:4])
	count := int(binary.LittleEndian.Uint16(raw[4:6]))
	if resType != 1 || count == 0 || len(raw) < 6+count*16 {
		return nil, fmt.Errorf("invalid ico header")
	}

	var grp bytes.Buffer
	_ = binary.Write(&grp, binary.LittleEndian, uint16(0))
	_ = binary.Write(&grp, binary.LittleEndian, uint16(1))
	_ = binary.Write(&grp, binary.LittleEndian, uint16(count))

	for i := 0; i < count; i++ {
		var e iconDirEntry
		pos := 6 + i*16
		_ = binary.Read(bytes.NewReader(raw[pos:pos+16]), binary.LittleEndian, &e)
		end := int(e.ImageOffset) + int(e.BytesInRes)
		if end > len(raw) {
			return nil, fmt.Errorf("ico entry %d out of bounds", i)
		}
		iconID := uint16(i + 1)
		items = append(items, resItem{
			typ:  rtIcon,
			id:   uint32(iconID),
			lang: 0x0409,
			data: raw[e.ImageOffset:end],
		})
		ge := grpIconDirEntry{
			Width:      e.Width,
			Height:     e.Height,
			ColorCount: e.ColorCount,
			Reserved:   e.Reserved,
			Planes:     e.Planes,
			BitCount:   e.BitCount,
			BytesInRes: e.BytesInRes,
			ID:         iconID,
		}
		_ = binary.Write(&grp, binary.LittleEndian, ge)
	}
	items = append(items, resItem{
		typ:  rtGroupIcon,
		id:   1,
		lang: 0x0409,
		data: grp.Bytes(),
	})
	return items, nil
}

func buildCOFFRsrc(items []resItem) ([]byte, error) {
	// Group items: type -> id -> lang (sorted ascending for Windows resource lookup)
	typeMap := map[uint32]map[uint32][]resItem{}
	for _, it := range items {
		if typeMap[it.typ] == nil {
			typeMap[it.typ] = map[uint32][]resItem{}
		}
		typeMap[it.typ][it.id] = append(typeMap[it.typ][it.id], it)
	}
	var types []uint32
	for t := range typeMap {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return types[i] < types[j] })

	totalIDs := 0
	totalLeaves := 0
	for _, t := range types {
		totalIDs += len(typeMap[t])
		for _, arr := range typeMap[t] {
			totalLeaves += len(arr)
		}
	}

	// Layout offsets inside .rsrc section:
	// Level 1 dir (16 + 8*len(types))
	// Level 2 dirs: len(types) dirs, each 16 + 8*len(ids_for_type)
	// Level 3 dirs: totalIDs dirs, each 16 + 8*len(langs_for_id)
	// Data entries: totalLeaves * 16
	// Raw payloads (4-byte aligned)
	pos := 16 + 8*len(types)
	typeDirOff := make(map[uint32]uint32, len(types))
	for _, t := range types {
		typeDirOff[t] = uint32(pos)
		pos += 16 + 8*len(typeMap[t])
	}
	type idKey struct{ t, id uint32 }
	idDirOff := make(map[idKey]uint32, totalIDs)
	for _, t := range types {
		var ids []uint32
		for id := range typeMap[t] {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			idDirOff[idKey{t, id}] = uint32(pos)
			pos += 16 + 8*len(typeMap[t][id])
		}
	}

	dataEntriesStart := pos
	dataPayloadsStart := dataEntriesStart + totalLeaves*16
	// Align payloads start to 8 bytes
	if pad := (8 - (dataPayloadsStart % 8)) % 8; pad > 0 {
		dataPayloadsStart += pad
	}

	var rsrc bytes.Buffer
	writeDirHeader := func(numIDs int) {
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(0)) // Characteristics
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(0)) // TimeDateStamp
		_ = binary.Write(&rsrc, binary.LittleEndian, uint16(0)) // MajorVersion
		_ = binary.Write(&rsrc, binary.LittleEndian, uint16(0)) // MinorVersion
		_ = binary.Write(&rsrc, binary.LittleEndian, uint16(0)) // NumberOfNamedEntries
		_ = binary.Write(&rsrc, binary.LittleEndian, uint16(numIDs))
	}
	writeDirEntry := func(nameOrID, offsetToData uint32) {
		_ = binary.Write(&rsrc, binary.LittleEndian, nameOrID)
		_ = binary.Write(&rsrc, binary.LittleEndian, offsetToData)
	}

	// 1. Root directory (Types)
	writeDirHeader(len(types))
	for _, t := range types {
		writeDirEntry(t, 0x80000000|typeDirOff[t])
	}

	// 2. Type directories (IDs)
	for _, t := range types {
		var ids []uint32
		for id := range typeMap[t] {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		writeDirHeader(len(ids))
		for _, id := range ids {
			writeDirEntry(id, 0x80000000|idDirOff[idKey{t, id}])
		}
	}

	// 3. ID directories (Langs) -> Data entries
	type leafRef struct {
		entryRsrcOffset uint32
		payloadOffset   uint32
		payloadSize     uint32
		data            []byte
	}
	var leaves []leafRef
	curDataEntryOff := dataEntriesStart
	curPayloadOff := dataPayloadsStart

	for _, t := range types {
		var ids []uint32
		for id := range typeMap[t] {
			ids = append(ids, id)
		}
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			arr := typeMap[t][id]
			sort.Slice(arr, func(i, j int) bool { return arr[i].lang < arr[j].lang })
			writeDirHeader(len(arr))
			for _, it := range arr {
				writeDirEntry(it.lang, uint32(curDataEntryOff))
				leaves = append(leaves, leafRef{
					entryRsrcOffset: uint32(curDataEntryOff),
					payloadOffset:   uint32(curPayloadOff),
					payloadSize:     uint32(len(it.data)),
					data:            it.data,
				})
				curDataEntryOff += 16
				alignedLen := len(it.data)
				if rem := alignedLen % 8; rem != 0 {
					alignedLen += 8 - rem
				}
				curPayloadOff += alignedLen
			}
		}
	}

	// 4. Data entries
	var relocOffsets []uint32
	for _, lf := range leaves {
		relocOffsets = append(relocOffsets, lf.entryRsrcOffset)
		_ = binary.Write(&rsrc, binary.LittleEndian, lf.payloadOffset) // relocated via ADDR32NB
		_ = binary.Write(&rsrc, binary.LittleEndian, lf.payloadSize)
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(0)) // CodePage
		_ = binary.Write(&rsrc, binary.LittleEndian, uint32(0)) // Reserved
	}

	// Pad up to dataPayloadsStart
	for rsrc.Len() < dataPayloadsStart {
		rsrc.WriteByte(0)
	}

	// 5. Payloads
	for _, lf := range leaves {
		rsrc.Write(lf.data)
		for rsrc.Len()%8 != 0 {
			rsrc.WriteByte(0)
		}
	}

	rsrcBytes := rsrc.Bytes()

	// Build COFF file: FileHeader(20) + SectionHeader(40) + RawData + Relocations(10*N) + SymbolTable(18) + StringTable(4)
	rawOffset := uint32(20 + 40)
	relocFileOff := rawOffset + uint32(len(rsrcBytes))
	symFileOff := relocFileOff + uint32(len(relocOffsets)*10)

	var out bytes.Buffer
	// IMAGE_FILE_HEADER
	_ = binary.Write(&out, binary.LittleEndian, uint16(imageFileMachineAMD64))
	_ = binary.Write(&out, binary.LittleEndian, uint16(1)) // NumberOfSections
	_ = binary.Write(&out, binary.LittleEndian, uint32(0)) // TimeDateStamp
	_ = binary.Write(&out, binary.LittleEndian, symFileOff)
	_ = binary.Write(&out, binary.LittleEndian, uint32(1)) // NumberOfSymbols
	_ = binary.Write(&out, binary.LittleEndian, uint16(0)) // SizeOfOptionalHeader
	_ = binary.Write(&out, binary.LittleEndian, uint16(0)) // Characteristics

	// IMAGE_SECTION_HEADER (.rsrc)
	var secName [8]byte
	copy(secName[:], ".rsrc")
	out.Write(secName[:])
	_ = binary.Write(&out, binary.LittleEndian, uint32(0)) // VirtualSize (0 in COFF obj)
	_ = binary.Write(&out, binary.LittleEndian, uint32(0)) // VirtualAddress
	_ = binary.Write(&out, binary.LittleEndian, uint32(len(rsrcBytes)))
	_ = binary.Write(&out, binary.LittleEndian, rawOffset)
	_ = binary.Write(&out, binary.LittleEndian, relocFileOff)
	_ = binary.Write(&out, binary.LittleEndian, uint32(0)) // PointerToLinenumbers
	_ = binary.Write(&out, binary.LittleEndian, uint16(len(relocOffsets)))
	_ = binary.Write(&out, binary.LittleEndian, uint16(0)) // NumberOfLinenumbers
	_ = binary.Write(&out, binary.LittleEndian, uint32(imageScnCntInitData|imageScnMemRead))

	out.Write(rsrcBytes)

	// IMAGE_RELOCATION entries (10 bytes each)
	for _, roff := range relocOffsets {
		_ = binary.Write(&out, binary.LittleEndian, roff)
		_ = binary.Write(&out, binary.LittleEndian, uint32(0)) // SymbolTableIndex (.rsrc section symbol)
		_ = binary.Write(&out, binary.LittleEndian, uint16(imageRelAMD64Addr32NB))
	}

	// IMAGE_SYMBOL (.rsrc section symbol, 18 bytes)
	out.Write(secName[:])
	_ = binary.Write(&out, binary.LittleEndian, uint32(0)) // Value
	_ = binary.Write(&out, binary.LittleEndian, int16(1))  // SectionNumber (1-based)
	_ = binary.Write(&out, binary.LittleEndian, uint16(0)) // Type
	_ = binary.Write(&out, binary.LittleEndian, uint8(imageSymClassStatic))
	_ = binary.Write(&out, binary.LittleEndian, uint8(0)) // NumberOfAuxSymbols

	// String table (4-byte size = 4, empty)
	_ = binary.Write(&out, binary.LittleEndian, uint32(4))

	return out.Bytes(), nil
}

func main() {
	root, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	icoPath := filepath.Join(root, "assets", "sharenet.ico")
	if len(os.Args) > 1 {
		icoPath = os.Args[1]
	}
	outPath := filepath.Join(root, "rsrc_windows_amd64.syso")
	if len(os.Args) > 2 {
		outPath = os.Args[2]
	}

	icoRaw, err := os.ReadFile(icoPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read ico: %v\n", err)
		os.Exit(1)
	}
	items, err := parseICO(icoRaw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "parse ico: %v\n", err)
		os.Exit(1)
	}
	items = append(items, resItem{
		typ:  rtManifest,
		id:   1,
		lang: 0x0409,
		data: []byte(appManifest),
	})

	coff, err := buildCOFFRsrc(items)
	if err != nil {
		fmt.Fprintf(os.Stderr, "build rsrc: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(outPath, coff, 0644); err != nil {
		fmt.Fprintf(os.Stderr, "write syso: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Generated %s (%d bytes, %d resource entries)\n", outPath, len(coff), len(items))
}
