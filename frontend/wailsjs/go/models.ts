export namespace main {
	
	export class Config {
	    text: string;
	    fontPath: string;
	    fontSize: number;
	    opacity: number;
	    angle: number;
	    color: string;
	    mode: string;
	    tileWidth: number;
	    tileHeight: number;
	    outputFormat: string;
	    jpegQuality: number;
	    stripMetadata: boolean;
	    recursive: boolean;
	    rotateOutput: number;
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.fontPath = source["fontPath"];
	        this.fontSize = source["fontSize"];
	        this.opacity = source["opacity"];
	        this.angle = source["angle"];
	        this.color = source["color"];
	        this.mode = source["mode"];
	        this.tileWidth = source["tileWidth"];
	        this.tileHeight = source["tileHeight"];
	        this.outputFormat = source["outputFormat"];
	        this.jpegQuality = source["jpegQuality"];
	        this.stripMetadata = source["stripMetadata"];
	        this.recursive = source["recursive"];
	        this.rotateOutput = source["rotateOutput"];
	    }
	}
	export class InputSummary {
	    paths: string[];
	    imageCount: number;
	    skipped: number;
	
	    static createFrom(source: any = {}) {
	        return new InputSummary(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.paths = source["paths"];
	        this.imageCount = source["imageCount"];
	        this.skipped = source["skipped"];
	    }
	}
	export class PreviewResult {
	    dataURL: string;
	    file: string;
	    width: number;
	    height: number;
	
	    static createFrom(source: any = {}) {
	        return new PreviewResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.dataURL = source["dataURL"];
	        this.file = source["file"];
	        this.width = source["width"];
	        this.height = source["height"];
	    }
	}
	export class ProcessResult {
	    total: number;
	    success: number;
	    failed: number;
	    outputDirectory: string;
	    errors: string[];
	    cancelled: boolean;
	
	    static createFrom(source: any = {}) {
	        return new ProcessResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.total = source["total"];
	        this.success = source["success"];
	        this.failed = source["failed"];
	        this.outputDirectory = source["outputDirectory"];
	        this.errors = source["errors"];
	        this.cancelled = source["cancelled"];
	    }
	}

}

